package mail

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type smtpTestServer struct {
	listener    net.Listener
	tlsConfig   *tls.Config
	mode        Security
	authFailure bool
	stall       bool
	done        chan struct{}
	mu          sync.Mutex
	message     string
}

func newSMTPTestServer(t *testing.T, mode Security, authFailure, stall bool) *smtpTestServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	certificate, _ := testCertificate(t)
	server := &smtpTestServer{listener: listener, tlsConfig: &tls.Config{Certificates: []tls.Certificate{certificate}}, mode: mode, authFailure: authFailure, stall: stall, done: make(chan struct{})}
	go server.serve()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-server.done:
		case <-time.After(time.Second):
		}
	})
	return server
}

func (s *smtpTestServer) serve() {
	defer close(s.done)
	connection, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer connection.Close()
	if s.stall {
		<-time.After(time.Second)
		return
	}
	secure := false
	if s.mode == SecurityTLS {
		connection = tls.Server(connection, s.tlsConfig)
		if err := connection.(*tls.Conn).Handshake(); err != nil {
			return
		}
		secure = true
	}
	reader := bufio.NewReader(connection)
	writer := bufio.NewWriter(connection)
	writeSMTPLine(writer, "220 localhost ESMTP")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.TrimSpace(line)
		upper := strings.ToUpper(command)
		switch {
		case strings.HasPrefix(upper, "EHLO") || strings.HasPrefix(upper, "HELO"):
			capabilities := []string{}
			if s.mode == SecuritySTARTTLS && !secure {
				capabilities = append(capabilities, "STARTTLS")
			}
			if s.authFailure {
				capabilities = append(capabilities, "AUTH PLAIN LOGIN")
			}
			if len(capabilities) == 0 {
				writeSMTPLine(writer, "250 localhost")
			} else {
				writeSMTPLine(writer, "250-localhost")
				for index, capability := range capabilities {
					separator := "-"
					if index == len(capabilities)-1 {
						separator = " "
					}
					writeSMTPLine(writer, "250"+separator+capability)
				}
			}
		case upper == "STARTTLS" && s.mode == SecuritySTARTTLS && !secure:
			writeSMTPLine(writer, "220 Ready to start TLS")
			connection = tls.Server(connection, s.tlsConfig)
			if err := connection.(*tls.Conn).Handshake(); err != nil {
				return
			}
			secure = true
			reader, writer = bufio.NewReader(connection), bufio.NewWriter(connection)
		case strings.HasPrefix(upper, "AUTH"):
			writeSMTPLine(writer, "535 5.7.8 Authentication failed")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			writeSMTPLine(writer, "250 2.1.0 Sender accepted")
		case strings.HasPrefix(upper, "RCPT TO:"):
			writeSMTPLine(writer, "250 2.1.5 Recipient accepted")
		case upper == "DATA":
			writeSMTPLine(writer, "354 End data with <CR><LF>.<CR><LF>")
			var message strings.Builder
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if line == ".\r\n" {
					break
				}
				message.WriteString(line)
			}
			s.mu.Lock()
			s.message = message.String()
			s.mu.Unlock()
			writeSMTPLine(writer, "250 2.0.0 Queued")
		case upper == "QUIT":
			writeSMTPLine(writer, "221 2.0.0 Bye")
			return
		default:
			writeSMTPLine(writer, "500 5.5.2 Unknown command")
		}
	}
}

func writeSMTPLine(writer *bufio.Writer, line string) {
	_, _ = writer.WriteString(line + "\r\n")
	_ = writer.Flush()
}

func (s *smtpTestServer) config(security Security) Config {
	_, portValue, _ := net.SplitHostPort(s.listener.Addr().String())
	port, _ := strconv.Atoi(portValue)
	return Config{Host: "localhost", Port: port, Security: security, FromAddress: "from@example.com", FromName: "VPS Panel"}
}

func testCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certificatePEM)
	return certificate, pool
}

func TestSMTPClientSecurityModesAndNoAuthentication(t *testing.T) {
	for _, mode := range []Security{SecurityTLS, SecuritySTARTTLS, SecurityNone} {
		t.Run(string(mode), func(t *testing.T) {
			server := newSMTPTestServer(t, mode, false, false)
			client := NewSMTPClient()
			if mode != SecurityNone {
				serverCertificate := server.tlsConfig.Certificates[0]
				leaf, err := x509.ParseCertificate(serverCertificate.Certificate[0])
				if err != nil {
					t.Fatal(err)
				}
				pool := x509.NewCertPool()
				pool.AddCert(leaf)
				client.tlsConfig = func(host string) *tls.Config {
					return &tls.Config{ServerName: host, RootCAs: pool, MinVersion: tls.VersionTLS12}
				}
			}
			message := Message{To: []string{"to@example.com"}, Subject: "测试", Text: "plain", HTML: "<p>html</p>"}
			if err := client.Send(t.Context(), server.config(mode), message); err != nil {
				t.Fatal(err)
			}
			server.mu.Lock()
			payload := server.message
			server.mu.Unlock()
			if !strings.Contains(payload, "multipart/alternative") || !strings.Contains(payload, "Subject:") {
				t.Fatalf("message payload = %q", payload)
			}
		})
	}
}

func TestSMTPClientClassifiesAuthenticationAndTLSFailures(t *testing.T) {
	authServer := newSMTPTestServer(t, SecurityNone, true, false)
	authConfig := authServer.config(SecurityNone)
	authConfig.Username, authConfig.Password = "user", "secret"
	message := Message{To: []string{"to@example.com"}, Subject: "Subject", Text: "Body"}
	var delivery *DeliveryError
	if err := NewSMTPClient().Send(t.Context(), authConfig, message); !errors.As(err, &delivery) || delivery.Kind != ErrorAuth {
		t.Fatalf("auth error = %v", err)
	}

	tlsServer := newSMTPTestServer(t, SecurityTLS, false, false)
	delivery = nil
	if err := NewSMTPClient().Send(t.Context(), tlsServer.config(SecurityTLS), message); !errors.As(err, &delivery) || delivery.Kind != ErrorCertificate {
		t.Fatalf("TLS error = %v", err)
	}

	plainServer := newSMTPTestServer(t, SecurityNone, false, false)
	delivery = nil
	if err := NewSMTPClient().Send(t.Context(), plainServer.config(SecuritySTARTTLS), message); !errors.As(err, &delivery) || delivery.Kind != ErrorSTARTTLS {
		t.Fatalf("STARTTLS error = %v", err)
	}
}

func TestSMTPClientTimeoutAndCancellation(t *testing.T) {
	message := Message{To: []string{"to@example.com"}, Subject: "Subject", Text: "Body"}
	timeoutServer := newSMTPTestServer(t, SecurityNone, false, true)
	client := NewSMTPClient()
	client.operationTimeout, client.overallTimeout = 30*time.Millisecond, 100*time.Millisecond
	var delivery *DeliveryError
	if err := client.Send(t.Context(), timeoutServer.config(SecurityNone), message); !errors.As(err, &delivery) || delivery.Kind != ErrorTimeout {
		t.Fatalf("timeout error = %v", err)
	}

	cancelServer := newSMTPTestServer(t, SecurityNone, false, true)
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(20*time.Millisecond, cancel)
	delivery = nil
	if err := NewSMTPClient().Send(ctx, cancelServer.config(SecurityNone), message); !errors.As(err, &delivery) || delivery.Kind != ErrorCanceled {
		t.Fatalf("cancel error = %v", err)
	}
}

func TestLoginAuthChallenges(t *testing.T) {
	auth := &loginAuth{username: "user", password: "secret", host: "localhost"}
	mechanism, initial, err := auth.Start(&smtp.ServerInfo{Name: "localhost", TLS: true, Auth: []string{"LOGIN"}})
	if err != nil || mechanism != "LOGIN" || initial != nil {
		t.Fatalf("start = %q %q %v", mechanism, initial, err)
	}
	username, _ := auth.Next([]byte("Username:"), true)
	password, _ := auth.Next([]byte("Password:"), true)
	if string(username) != "user" || string(password) != "secret" {
		t.Fatalf("credentials = %q / %q", username, password)
	}
}
