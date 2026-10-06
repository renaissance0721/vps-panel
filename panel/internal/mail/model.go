package mail

import (
	"context"
	"errors"
	"fmt"
	stdmail "net/mail"
	"net/netip"
	"strings"
)

type Security string

const (
	SecurityTLS      Security = "tls"
	SecuritySTARTTLS Security = "starttls"
	SecurityNone     Security = "none"
)

type Settings struct {
	Enabled            bool     `json:"enabled"`
	Host               string   `json:"host"`
	Port               int      `json:"port"`
	Security           Security `json:"security"`
	Username           string   `json:"username"`
	PasswordConfigured bool     `json:"password_configured"`
	FromAddress        string   `json:"from_address"`
	FromName           string   `json:"from_name"`
	ReplyTo            string   `json:"reply_to"`
}

type Update struct {
	Enabled     bool     `json:"enabled"`
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	Security    Security `json:"security"`
	Username    string   `json:"username"`
	Password    string   `json:"password"`
	FromAddress string   `json:"from_address"`
	FromName    string   `json:"from_name"`
	ReplyTo     string   `json:"reply_to"`
}

type TestInput struct {
	Update
	To string `json:"to"`
}

type Config struct {
	Enabled     bool
	Host        string
	Port        int
	Security    Security
	Username    string
	Password    string
	FromAddress string
	FromName    string
	ReplyTo     string
}

type Message struct {
	To      []string
	Subject string
	Text    string
	HTML    string
}

type Sender interface {
	Send(context.Context, Config, Message) error
}

var (
	ErrDisabled              = errors.New("邮件服务未启用")
	ErrInvalidSettings       = errors.New("邮件设置无效")
	ErrCredentialUnavailable = errors.New("SMTP 凭据无法解密，请重新填写密码")
)

type ErrorKind string

const (
	ErrorConnect     ErrorKind = "connect"
	ErrorTLS         ErrorKind = "tls"
	ErrorCertificate ErrorKind = "certificate"
	ErrorSTARTTLS    ErrorKind = "starttls"
	ErrorAuth        ErrorKind = "auth"
	ErrorSender      ErrorKind = "sender"
	ErrorRecipient   ErrorKind = "recipient"
	ErrorTimeout     ErrorKind = "timeout"
	ErrorCanceled    ErrorKind = "canceled"
	ErrorProtocol    ErrorKind = "protocol"
)

type DeliveryError struct {
	Kind  ErrorKind
	cause error
}

func (e *DeliveryError) Error() string {
	switch e.Kind {
	case ErrorConnect:
		return "无法连接 SMTP Server"
	case ErrorTLS:
		return "SMTP TLS 握手失败"
	case ErrorCertificate:
		return "SMTP 证书验证失败"
	case ErrorSTARTTLS:
		return "SMTP Server 不支持 STARTTLS"
	case ErrorAuth:
		return "SMTP AUTH 认证失败"
	case ErrorSender:
		return "发件人被 SMTP Server 拒绝"
	case ErrorRecipient:
		return "收件人被 SMTP Server 拒绝"
	case ErrorTimeout:
		return "SMTP Server 超时"
	case ErrorCanceled:
		return "SMTP 发送已取消"
	default:
		return "SMTP Server 返回无效响应"
	}
}

func (e *DeliveryError) Unwrap() error { return e.cause }

func normalizeUpdate(value Update) Update {
	value.Host = strings.TrimSpace(value.Host)
	value.Username = strings.TrimSpace(value.Username)
	value.FromAddress = strings.TrimSpace(value.FromAddress)
	value.FromName = strings.TrimSpace(value.FromName)
	value.ReplyTo = strings.TrimSpace(value.ReplyTo)
	return value
}

func configFromUpdate(value Update) Config {
	return Config{
		Enabled: value.Enabled, Host: value.Host, Port: value.Port, Security: value.Security,
		Username: value.Username, Password: value.Password, FromAddress: value.FromAddress,
		FromName: value.FromName, ReplyTo: value.ReplyTo,
	}
}

func validateConfig(value Config, passwordConfigured bool) error {
	switch {
	case !validSMTPHost(value.Host):
		return fmt.Errorf("%w：SMTP 服务器无效", ErrInvalidSettings)
	case value.Port < 1 || value.Port > 65535:
		return fmt.Errorf("%w：端口必须在 1–65535 之间", ErrInvalidSettings)
	case value.Security != SecurityTLS && value.Security != SecuritySTARTTLS && value.Security != SecurityNone:
		return fmt.Errorf("%w：加密方式必须是 tls、starttls 或 none", ErrInvalidSettings)
	case len(value.Username) > 320 || containsHeaderControl(value.Username):
		return fmt.Errorf("%w：SMTP 用户名无效", ErrInvalidSettings)
	case len(value.Password) > 1024:
		return fmt.Errorf("%w：SMTP 密码过长", ErrInvalidSettings)
	case value.Username != "" && !passwordConfigured:
		return fmt.Errorf("%w：配置 SMTP 用户名时必须提供密码", ErrInvalidSettings)
	case !validMailbox(value.FromAddress):
		return fmt.Errorf("%w：发件地址无效", ErrInvalidSettings)
	case value.ReplyTo != "" && !validMailbox(value.ReplyTo):
		return fmt.Errorf("%w：Reply-To 地址无效", ErrInvalidSettings)
	case len(value.FromName) > 100 || containsHeaderControl(value.FromName):
		return fmt.Errorf("%w：发件人名称无效", ErrInvalidSettings)
	}
	return nil
}

func validateMessage(value Message) error {
	if len(value.To) == 0 || len(value.To) > 100 {
		return fmt.Errorf("%w：收件地址无效", ErrInvalidSettings)
	}
	for _, address := range value.To {
		if !validMailbox(address) {
			return fmt.Errorf("%w：收件地址无效", ErrInvalidSettings)
		}
	}
	if value.Subject == "" || len(value.Subject) > 200 || containsHeaderControl(value.Subject) {
		return fmt.Errorf("%w：邮件主题无效", ErrInvalidSettings)
	}
	if value.Text == "" && value.HTML == "" {
		return fmt.Errorf("%w：邮件正文不能为空", ErrInvalidSettings)
	}
	return nil
}

func validMailbox(value string) bool {
	if value == "" || len(value) > 320 || strings.ContainsAny(value, "\r\n") {
		return false
	}
	parsed, err := stdmail.ParseAddress(value)
	return err == nil && parsed.Name == "" && parsed.Address == value
}

func validSMTPHost(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	if _, err := netip.ParseAddr(value); err == nil {
		return true
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func containsHeaderControl(value string) bool {
	for _, character := range value {
		if character < 32 || character == 127 {
			return true
		}
	}
	return false
}
