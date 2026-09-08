package imap

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
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNetworkProberSupportsImplicitTLSAndBoundedCapabilities(t *testing.T) {
	serverTLS, roots := probeCertificate(t)
	imapServer := serveIMAP(t, serverTLS, TLSImplicit, true)
	smtpServer := serveSMTP(t, serverTLS, TLSImplicit, true)
	prober, _ := NewNetworkProber(2*time.Second, roots)
	input := probeInput(imapServer, smtpServer, TLSImplicit)
	capabilities, err := prober.Probe(context.Background(), input)
	if err != nil || !capabilities["imap.tls"] || !capabilities["imap.idle"] || !capabilities["imap.move"] || !capabilities["smtp.tls"] || !capabilities["smtp.8bitmime"] || !capabilities["smtp.smtputf8"] {
		t.Fatalf("capabilities=%+v error=%v", capabilities, err)
	}
}

func TestNetworkProberNegotiatesSTARTTLSBeforeAuthentication(t *testing.T) {
	serverTLS, roots := probeCertificate(t)
	imapServer := serveIMAP(t, serverTLS, TLSStartTLS, true)
	smtpServer := serveSMTP(t, serverTLS, TLSStartTLS, true)
	prober, _ := NewNetworkProber(2*time.Second, roots)
	capabilities, err := prober.Probe(context.Background(), probeInput(imapServer, smtpServer, TLSStartTLS))
	if err != nil || !capabilities["imap.starttls"] || !capabilities["smtp.starttls"] || !capabilities["smtp.dsn"] || !capabilities["smtp.size"] {
		t.Fatalf("STARTTLS capabilities=%+v error=%v", capabilities, err)
	}
}

func TestNetworkProberClassifiesCertificateAuthenticationAndTimeout(t *testing.T) {
	serverTLS, _ := probeCertificate(t)
	t.Run("certificate", func(t *testing.T) {
		imapServer := serveIMAP(t, serverTLS, TLSImplicit, true)
		prober, _ := NewNetworkProber(time.Second, x509.NewCertPool())
		input := probeInput(imapServer, "127.0.0.1:1", TLSImplicit)
		if _, err := prober.Probe(context.Background(), input); !errors.Is(err, ErrTLSIdentity) {
			t.Fatalf("certificate error=%v", err)
		}
	})
	t.Run("authentication", func(t *testing.T) {
		_, roots := probeCertificateWithConfig(t, serverTLS)
		imapServer := serveIMAP(t, serverTLS, TLSImplicit, false)
		prober, _ := NewNetworkProber(time.Second, roots)
		input := probeInput(imapServer, "127.0.0.1:1", TLSImplicit)
		if _, err := prober.Probe(context.Background(), input); !errors.Is(err, ErrAuthentication) {
			t.Fatalf("authentication error=%v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = listener.Close() })
		go func() {
			connection, acceptErr := listener.Accept()
			if acceptErr == nil {
				defer connection.Close()
				time.Sleep(250 * time.Millisecond)
			}
		}()
		prober, _ := NewNetworkProber(50*time.Millisecond, nil)
		input := probeInput(listener.Addr().String(), "127.0.0.1:1", TLSStartTLS)
		if _, err := prober.Probe(context.Background(), input); !errors.Is(err, ErrTimeout) {
			t.Fatalf("timeout error=%v", err)
		}
	})
	t.Run("STARTTLS response timeout", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = listener.Close() })
		go func() {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			defer connection.Close()
			_, _ = connection.Write([]byte("* OK fixture ready\r\n"))
			_, _ = bufio.NewReader(connection).ReadString('\n')
			time.Sleep(250 * time.Millisecond)
		}()
		prober, _ := NewNetworkProber(50*time.Millisecond, nil)
		input := probeInput(listener.Addr().String(), "127.0.0.1:1", TLSStartTLS)
		if _, err := prober.Probe(context.Background(), input); !errors.Is(err, ErrTimeout) {
			t.Fatalf("STARTTLS timeout error=%v", err)
		}
	})
}

func TestSMTPAuthenticatorSupportsPasswordMechanismsAndRejectsUnknownOnes(t *testing.T) {
	if _, err := smtpAuthenticator("PLAIN LOGIN", "owner", "secret", "smtp.example.test"); err != nil {
		t.Fatalf("PLAIN authenticator: %v", err)
	}
	authenticator, err := smtpAuthenticator("XOAUTH2 LOGIN", "owner", "secret", "smtp.example.test")
	if err != nil {
		t.Fatalf("LOGIN authenticator: %v", err)
	}
	if _, ok := authenticator.(loginAuth); !ok {
		t.Fatalf("authenticator type = %T", authenticator)
	}
	if _, err := smtpAuthenticator("XOAUTH2", "owner", "secret", "smtp.example.test"); !errors.Is(err, ErrCapability) {
		t.Fatalf("unsupported mechanisms error=%v", err)
	}
}

func TestCapabilityParserIgnoresUnboundedProviderValues(t *testing.T) {
	capabilities := imapCapabilities([]string{"* CAPABILITY IMAP4rev1 IDLE UIDPLUS PRIVATE=secret QRESYNC"}, TLSImplicit)
	if !capabilities["imap.idle"] || !capabilities["imap.uidplus"] || !capabilities["imap.qresync"] || capabilities["PRIVATE=secret"] || len(capabilities) != 5 {
		t.Fatalf("capabilities=%+v", capabilities)
	}
}

func probeInput(imapAddress, smtpAddress string, mode TLSMode) ConnectInput {
	imapHost, imapPort, _ := net.SplitHostPort(imapAddress)
	smtpHost, smtpPort, _ := net.SplitHostPort(smtpAddress)
	return ConnectInput{
		UserID: "0199ed3b-c950-7000-8000-000000000001", DisplayName: "Fixture", Username: "owner@example.test", Password: "app-password",
		IMAP: ServerConfig{Host: hostForTLS(imapHost), Port: integerPort(imapPort), TLSMode: mode},
		SMTP: ServerConfig{Host: hostForTLS(smtpHost), Port: integerPort(smtpPort), TLSMode: mode},
	}
}

func hostForTLS(host string) string {
	if host == "127.0.0.1" {
		return "localhost"
	}
	return host
}

func integerPort(value string) int {
	port, _ := strconv.Atoi(value)
	return port
}

func probeCertificate(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(certificatePEM)
	return &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}, roots
}

func probeCertificateWithConfig(t *testing.T, config *tls.Config) (*tls.Config, *x509.CertPool) {
	t.Helper()
	certificate, err := x509.ParseCertificate(config.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	return config, roots
}

func serveIMAP(t *testing.T, config *tls.Config, mode TLSMode, authenticate bool) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		if mode == TLSImplicit {
			connection = tls.Server(connection, config)
			if handshakeErr := connection.(*tls.Conn).Handshake(); handshakeErr != nil {
				return
			}
		}
		reader, writer := bufio.NewReader(connection), bufio.NewWriter(connection)
		_, _ = writer.WriteString("* OK fixture ready\r\n")
		_ = writer.Flush()
		if mode == TLSStartTLS {
			line, _ := reader.ReadString('\n')
			if !strings.HasPrefix(line, "A001 STARTTLS") {
				return
			}
			_, _ = writer.WriteString("A001 OK begin TLS\r\n")
			_ = writer.Flush()
			connection = tls.Server(connection, config)
			if handshakeErr := connection.(*tls.Conn).Handshake(); handshakeErr != nil {
				return
			}
			reader, writer = bufio.NewReader(connection), bufio.NewWriter(connection)
		}
		if line, _ := reader.ReadString('\n'); !strings.HasPrefix(line, "A002 CAPABILITY") {
			return
		}
		_, _ = writer.WriteString("* CAPABILITY IMAP4rev1 IDLE UIDPLUS MOVE PRIVATE=ignored\r\nA002 OK capability complete\r\n")
		_ = writer.Flush()
		if line, _ := reader.ReadString('\n'); !strings.HasPrefix(line, "A003 LOGIN") {
			return
		}
		if authenticate {
			_, _ = writer.WriteString("A003 OK authenticated\r\n")
		} else {
			_, _ = writer.WriteString("A003 NO authentication failed\r\n")
		}
		_ = writer.Flush()
	}()
	return listener.Addr().String()
}

func serveSMTP(t *testing.T, config *tls.Config, mode TLSMode, authenticate bool) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		if mode == TLSImplicit {
			connection = tls.Server(connection, config)
			if handshakeErr := connection.(*tls.Conn).Handshake(); handshakeErr != nil {
				return
			}
		}
		reader, writer := bufio.NewReader(connection), bufio.NewWriter(connection)
		_, _ = writer.WriteString("220 localhost fixture\r\n")
		_ = writer.Flush()
		serveEHLO := func(includeSTARTTLS bool) bool {
			line, readErr := reader.ReadString('\n')
			if readErr != nil || !strings.HasPrefix(line, "EHLO ") {
				return false
			}
			_, _ = writer.WriteString("250-localhost\r\n")
			if includeSTARTTLS {
				_, _ = writer.WriteString("250-STARTTLS\r\n")
			}
			_, _ = writer.WriteString("250-AUTH PLAIN\r\n250-8BITMIME\r\n250-SMTPUTF8\r\n250-DSN\r\n250 SIZE 1048576\r\n")
			_ = writer.Flush()
			return true
		}
		if !serveEHLO(mode == TLSStartTLS) {
			return
		}
		if mode == TLSStartTLS {
			line, _ := reader.ReadString('\n')
			if !strings.HasPrefix(line, "STARTTLS") {
				return
			}
			_, _ = writer.WriteString("220 begin TLS\r\n")
			_ = writer.Flush()
			connection = tls.Server(connection, config)
			if handshakeErr := connection.(*tls.Conn).Handshake(); handshakeErr != nil {
				return
			}
			reader, writer = bufio.NewReader(connection), bufio.NewWriter(connection)
			if !serveEHLO(false) {
				return
			}
		}
		line, _ := reader.ReadString('\n')
		if !strings.HasPrefix(line, "AUTH PLAIN") {
			return
		}
		if authenticate {
			_, _ = writer.WriteString("235 2.7.0 authenticated\r\n")
		} else {
			_, _ = writer.WriteString("535 5.7.8 authentication failed\r\n")
		}
		_ = writer.Flush()
		if authenticate {
			_, _ = reader.ReadString('\n')
			_, _ = writer.WriteString("221 2.0.0 bye\r\n")
			_ = writer.Flush()
		}
	}()
	return listener.Addr().String()
}
