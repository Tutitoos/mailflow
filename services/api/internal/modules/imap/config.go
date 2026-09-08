package imap

import (
	"net"
	stdmail "net/mail"
	"strings"
)

type TLSMode string

const (
	TLSImplicit TLSMode = "implicit"
	TLSStartTLS TLSMode = "starttls"
)

type ServerConfig struct {
	Host    string  `json:"host"`
	Port    int     `json:"port"`
	TLSMode TLSMode `json:"tlsMode"`
}

type ConnectInput struct {
	UserID      string       `json:"-"`
	DisplayName string       `json:"displayName"`
	Username    string       `json:"username"`
	Password    string       `json:"password"`
	IMAP        ServerConfig `json:"imap"`
	SMTP        ServerConfig `json:"smtp"`
}

// ICloudConnectInput deliberately names the only accepted credential. The
// public iCloud setup contract never asks for an Apple Account password or
// accepts caller-controlled server endpoints.
type ICloudConnectInput struct {
	UserID              string `json:"-"`
	DisplayName         string `json:"displayName"`
	Email               string `json:"email"`
	AppSpecificPassword string `json:"appSpecificPassword"`
}

type storedCredentials struct {
	Username string       `json:"username"`
	Password string       `json:"password"`
	IMAP     ServerConfig `json:"imap"`
	SMTP     ServerConfig `json:"smtp"`
}

func (input ConnectInput) valid() bool {
	return bounded(input.UserID, 128) && bounded(input.DisplayName, 256) && bounded(input.Username, 512) && bounded(input.Password, 4096) && input.IMAP.valid() && input.SMTP.valid()
}

func (input ICloudConnectInput) valid() bool {
	if !bounded(input.UserID, 128) || !bounded(input.DisplayName, 256) || !bounded(input.Email, 320) || !bounded(input.AppSpecificPassword, 4096) {
		return false
	}
	address, err := stdmail.ParseAddress(strings.TrimSpace(input.Email))
	return err == nil && strings.EqualFold(address.Address, strings.TrimSpace(input.Email))
}

func (input ICloudConnectInput) connectInput() ConnectInput {
	return ConnectInput{
		UserID: input.UserID, DisplayName: input.DisplayName, Username: input.Email, Password: input.AppSpecificPassword,
		IMAP: ServerConfig{Host: "imap.mail.me.com", Port: 993, TLSMode: TLSImplicit},
		SMTP: ServerConfig{Host: "smtp.mail.me.com", Port: 587, TLSMode: TLSStartTLS},
	}
}

func (server ServerConfig) valid() bool {
	host := strings.TrimSpace(server.Host)
	if host == "" || len(host) > 253 || server.Port < 1 || server.Port > 65535 || (server.TLSMode != TLSImplicit && server.TLSMode != TLSStartTLS) {
		return false
	}
	if strings.ContainsAny(host, "\x00\r\n/\\") {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func bounded(value string, maximum int) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && len(trimmed) <= maximum && !strings.ContainsAny(trimmed, "\x00\r\n")
}
