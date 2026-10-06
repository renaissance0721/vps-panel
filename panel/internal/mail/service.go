package mail

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	testSubject = "VPS Panel 邮件服务测试"
	testBody    = "这是一封由 VPS Panel 发出的 SMTP 测试邮件。\n如果你收到此邮件，说明当前邮件服务配置可以正常使用。"
)

type Service struct {
	db      *sql.DB
	secrets *secretStore
	sender  Sender
	now     func() time.Time
}

type storedSettings struct {
	Settings
	passwordCiphertext string
}

func NewService(db *sql.DB, keyPath string, sender Sender) *Service {
	if sender == nil {
		sender = NewSMTPClient()
	}
	return &Service{db: db, secrets: newSecretStore(keyPath), sender: sender, now: time.Now}
}

func (s *Service) Settings(ctx context.Context) (Settings, error) {
	value, err := s.load(ctx)
	return value.Settings, err
}

func (s *Service) Save(ctx context.Context, value Update) (Settings, error) {
	value = normalizeUpdate(value)
	current, err := s.load(ctx)
	if err != nil {
		return Settings{}, err
	}
	ciphertext := current.passwordCiphertext
	passwordConfigured := ciphertext != ""
	if value.Password != "" {
		passwordConfigured = true
	}
	config := configFromUpdate(value)
	if err := validateConfig(config, passwordConfigured); err != nil {
		return Settings{}, err
	}
	if value.Password != "" {
		ciphertext, err = s.secrets.Encrypt(value.Password)
		if err != nil {
			return Settings{}, fmt.Errorf("encrypt SMTP password: %w", err)
		}
	}
	_, err = s.db.ExecContext(ctx, `UPDATE mail_settings SET enabled = ?, host = ?, port = ?, security = ?,
		username = ?, password_ciphertext = ?, from_address = ?, from_name = ?, reply_to = ?, updated_at = ? WHERE id = 1`,
		value.Enabled, value.Host, value.Port, value.Security, value.Username, ciphertext,
		value.FromAddress, value.FromName, value.ReplyTo, s.now().UTC().Unix())
	if err != nil {
		return Settings{}, fmt.Errorf("save mail settings: %w", err)
	}
	return Settings{
		Enabled: value.Enabled, Host: value.Host, Port: value.Port, Security: value.Security,
		Username: value.Username, PasswordConfigured: passwordConfigured, FromAddress: value.FromAddress,
		FromName: value.FromName, ReplyTo: value.ReplyTo,
	}, nil
}

func (s *Service) Send(ctx context.Context, message Message) error {
	stored, err := s.load(ctx)
	if err != nil {
		return err
	}
	if !stored.Enabled {
		return ErrDisabled
	}
	config, err := s.resolveConfig(stored, "")
	if err != nil {
		return err
	}
	if err := validateConfig(config, stored.PasswordConfigured); err != nil {
		return err
	}
	if err := validateMessage(message); err != nil {
		return err
	}
	return s.sender.Send(ctx, config, message)
}

func (s *Service) SendTest(ctx context.Context, input TestInput) error {
	input.Update = normalizeUpdate(input.Update)
	input.To = strings.TrimSpace(input.To)
	stored, err := s.load(ctx)
	if err != nil {
		return err
	}
	config := configFromUpdate(input.Update)
	passwordConfigured := input.Password != "" || stored.PasswordConfigured
	if input.Password == "" && config.Username != "" {
		config, err = s.resolveConfigWith(config, stored.passwordCiphertext)
		if err != nil {
			return err
		}
	}
	if err := validateConfig(config, passwordConfigured); err != nil {
		return err
	}
	message := Message{To: []string{input.To}, Subject: testSubject, Text: testBody}
	if err := validateMessage(message); err != nil {
		return err
	}
	return s.sender.Send(ctx, config, message)
}

func (s *Service) load(ctx context.Context) (storedSettings, error) {
	var value storedSettings
	err := s.db.QueryRowContext(ctx, `SELECT enabled, host, port, security, username, password_ciphertext,
		from_address, from_name, reply_to FROM mail_settings WHERE id = 1`).Scan(
		&value.Enabled, &value.Host, &value.Port, &value.Security, &value.Username, &value.passwordCiphertext,
		&value.FromAddress, &value.FromName, &value.ReplyTo)
	if err != nil {
		return storedSettings{}, fmt.Errorf("read mail settings: %w", err)
	}
	value.PasswordConfigured = value.passwordCiphertext != ""
	return value, nil
}

func (s *Service) resolveConfig(value storedSettings, password string) (Config, error) {
	config := Config{
		Enabled: value.Enabled, Host: value.Host, Port: value.Port, Security: value.Security,
		Username: value.Username, Password: password, FromAddress: value.FromAddress,
		FromName: value.FromName, ReplyTo: value.ReplyTo,
	}
	return s.resolveConfigWith(config, value.passwordCiphertext)
}

func (s *Service) resolveConfigWith(config Config, ciphertext string) (Config, error) {
	if config.Username == "" || config.Password != "" {
		return config, nil
	}
	if ciphertext == "" {
		return config, nil
	}
	password, err := s.secrets.Decrypt(ciphertext)
	if err != nil {
		return Config{}, errors.Join(ErrCredentialUnavailable, err)
	}
	config.Password = password
	return config, nil
}
