package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	stdmail "net/mail"
	"net/smtp"
	"net/textproto"
	"slices"
	"strings"
	"time"
)

const (
	defaultConnectTimeout   = 10 * time.Second
	defaultOperationTimeout = 15 * time.Second
	defaultOverallTimeout   = 30 * time.Second
)

type SMTPClient struct {
	connectTimeout   time.Duration
	operationTimeout time.Duration
	overallTimeout   time.Duration
	tlsConfig        func(string) *tls.Config
}

func NewSMTPClient() *SMTPClient {
	return &SMTPClient{
		connectTimeout: defaultConnectTimeout, operationTimeout: defaultOperationTimeout,
		overallTimeout: defaultOverallTimeout, tlsConfig: defaultTLSConfig,
	}
}

func (s *SMTPClient) Send(parent context.Context, config Config, message Message) error {
	ctx, cancel := context.WithTimeout(parent, s.overallTimeout)
	defer cancel()
	address := net.JoinHostPort(config.Host, fmt.Sprint(config.Port))
	dialer := net.Dialer{Timeout: s.connectTimeout}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return classifyDeliveryError(ctx, ErrorConnect, err)
	}
	defer connection.Close()
	canceled := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-canceled:
		}
	}()
	defer close(canceled)

	if err := setOperationDeadline(connection, ctx, s.operationTimeout); err != nil {
		return classifyDeliveryError(ctx, ErrorConnect, err)
	}
	if config.Security == SecurityTLS {
		tlsConnection := tls.Client(connection, s.tlsConfig(config.Host))
		if err := tlsConnection.HandshakeContext(ctx); err != nil {
			return classifyDeliveryError(ctx, ErrorTLS, err)
		}
		connection = tlsConnection
	}
	client, err := smtp.NewClient(connection, config.Host)
	if err != nil {
		return classifyDeliveryError(ctx, ErrorProtocol, err)
	}
	defer client.Close()

	if config.Security == SecuritySTARTTLS {
		if err := setOperationDeadline(connection, ctx, s.operationTimeout); err != nil {
			return classifyDeliveryError(ctx, ErrorSTARTTLS, err)
		}
		if supported, _ := client.Extension("STARTTLS"); !supported {
			return &DeliveryError{Kind: ErrorSTARTTLS}
		}
		if err := client.StartTLS(s.tlsConfig(config.Host)); err != nil {
			return classifyDeliveryError(ctx, ErrorTLS, err)
		}
	}

	if config.Username != "" {
		if err := setOperationDeadline(connection, ctx, s.operationTimeout); err != nil {
			return classifyDeliveryError(ctx, ErrorAuth, err)
		}
		supported, mechanisms := client.Extension("AUTH")
		if !supported {
			return &DeliveryError{Kind: ErrorAuth}
		}
		auth := smtpAuth(config, mechanisms)
		if auth == nil {
			return &DeliveryError{Kind: ErrorAuth}
		}
		if err := client.Auth(auth); err != nil {
			return classifyDeliveryError(ctx, ErrorAuth, err)
		}
	}

	if err := setOperationDeadline(connection, ctx, s.operationTimeout); err != nil {
		return classifyDeliveryError(ctx, ErrorSender, err)
	}
	if err := client.Mail(config.FromAddress); err != nil {
		return classifyDeliveryError(ctx, ErrorSender, err)
	}
	for _, recipient := range message.To {
		if err := setOperationDeadline(connection, ctx, s.operationTimeout); err != nil {
			return classifyDeliveryError(ctx, ErrorRecipient, err)
		}
		if err := client.Rcpt(recipient); err != nil {
			return classifyDeliveryError(ctx, ErrorRecipient, err)
		}
	}
	if err := setOperationDeadline(connection, ctx, s.operationTimeout); err != nil {
		return classifyDeliveryError(ctx, ErrorProtocol, err)
	}
	writer, err := client.Data()
	if err != nil {
		return classifyDeliveryError(ctx, ErrorProtocol, err)
	}
	payload, err := buildMessage(config, message)
	if err != nil {
		_ = writer.Close()
		return err
	}
	_, writeErr := writer.Write(payload)
	closeErr := writer.Close()
	if writeErr != nil {
		return classifyDeliveryError(ctx, ErrorProtocol, writeErr)
	}
	if closeErr != nil {
		return classifyDeliveryError(ctx, ErrorProtocol, closeErr)
	}
	_ = setOperationDeadline(connection, ctx, s.operationTimeout)
	_ = client.Quit()
	return nil
}

func defaultTLSConfig(host string) *tls.Config {
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
}

func setOperationDeadline(connection net.Conn, ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	return connection.SetDeadline(deadline)
}

func classifyDeliveryError(ctx context.Context, fallback ErrorKind, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return &DeliveryError{Kind: ErrorCanceled, cause: err}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &DeliveryError{Kind: ErrorTimeout, cause: err}
	}
	var netError net.Error
	if errors.As(err, &netError) && netError.Timeout() {
		return &DeliveryError{Kind: ErrorTimeout, cause: err}
	}
	var verificationError *tls.CertificateVerificationError
	var hostnameError x509.HostnameError
	var authorityError x509.UnknownAuthorityError
	var invalidError x509.CertificateInvalidError
	if errors.As(err, &verificationError) || errors.As(err, &hostnameError) ||
		errors.As(err, &authorityError) || errors.As(err, &invalidError) {
		return &DeliveryError{Kind: ErrorCertificate, cause: err}
	}
	return &DeliveryError{Kind: fallback, cause: err}
}

func smtpAuth(config Config, advertised string) smtp.Auth {
	mechanisms := strings.Fields(strings.ToUpper(advertised))
	if slices.Contains(mechanisms, "CRAM-MD5") {
		return smtp.CRAMMD5Auth(config.Username, config.Password)
	}
	if slices.Contains(mechanisms, "PLAIN") {
		if config.Security != SecurityNone {
			return smtp.PlainAuth("", config.Username, config.Password, config.Host)
		}
		return &plainAuth{username: config.Username, password: config.Password, host: config.Host}
	}
	if slices.Contains(mechanisms, "LOGIN") {
		return &loginAuth{username: config.Username, password: config.Password, host: config.Host}
	}
	return nil
}

type plainAuth struct{ username, password, host string }

func (a *plainAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !strings.EqualFold(server.Name, a.host) {
		return "", nil, errors.New("SMTP server name mismatch")
	}
	return "PLAIN", []byte("\x00" + a.username + "\x00" + a.password), nil
}

func (*plainAuth) Next([]byte, bool) ([]byte, error) { return nil, nil }

type loginAuth struct {
	username string
	password string
	host     string
	step     int
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !strings.EqualFold(server.Name, a.host) {
		return "", nil, errors.New("SMTP server name mismatch")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(_ []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	a.step++
	switch a.step {
	case 1:
		return []byte(a.username), nil
	case 2:
		return []byte(a.password), nil
	default:
		return nil, errors.New("unexpected SMTP LOGIN challenge")
	}
}

func buildMessage(config Config, message Message) ([]byte, error) {
	var output bytes.Buffer
	from := (&stdmail.Address{Name: config.FromName, Address: config.FromAddress}).String()
	recipients := make([]string, 0, len(message.To))
	for _, address := range message.To {
		recipients = append(recipients, (&stdmail.Address{Address: address}).String())
	}
	writeHeader(&output, "From", from)
	writeHeader(&output, "To", strings.Join(recipients, ", "))
	if config.ReplyTo != "" {
		writeHeader(&output, "Reply-To", (&stdmail.Address{Address: config.ReplyTo}).String())
	}
	writeHeader(&output, "Subject", mime.QEncoding.Encode("utf-8", message.Subject))
	writeHeader(&output, "Date", time.Now().Format(time.RFC1123Z))
	writeHeader(&output, "MIME-Version", "1.0")
	if message.HTML == "" {
		writeHeader(&output, "Content-Type", `text/plain; charset="utf-8"`)
		writeHeader(&output, "Content-Transfer-Encoding", "quoted-printable")
		output.WriteString("\r\n")
		writer := quotedprintable.NewWriter(&output)
		_, err := io.WriteString(writer, message.Text)
		closeErr := writer.Close()
		if err != nil {
			return nil, err
		}
		return output.Bytes(), closeErr
	}
	var body bytes.Buffer
	multipartWriter := multipart.NewWriter(&body)
	for _, part := range []struct{ contentType, value string }{
		{`text/plain; charset="utf-8"`, message.Text},
		{`text/html; charset="utf-8"`, message.HTML},
	} {
		if part.value == "" {
			continue
		}
		header := textproto.MIMEHeader{}
		header.Set("Content-Type", part.contentType)
		header.Set("Content-Transfer-Encoding", "quoted-printable")
		partWriter, err := multipartWriter.CreatePart(header)
		if err != nil {
			return nil, err
		}
		encoded := quotedprintable.NewWriter(partWriter)
		if _, err := io.WriteString(encoded, part.value); err != nil {
			return nil, err
		}
		if err := encoded.Close(); err != nil {
			return nil, err
		}
	}
	if err := multipartWriter.Close(); err != nil {
		return nil, err
	}
	writeHeader(&output, "Content-Type", `multipart/alternative; boundary="`+multipartWriter.Boundary()+`"`)
	output.WriteString("\r\n")
	output.Write(body.Bytes())
	return output.Bytes(), nil
}

func writeHeader(output *bytes.Buffer, name, value string) {
	fmt.Fprintf(output, "%s: %s\r\n", name, value)
}
