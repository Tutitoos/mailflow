package imap

import (
	"net"
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

type storedCredentials struct {
	Username string       `json:"username"`
	Password string       `json:"password"`
	IMAP     ServerConfig `json:"imap"`
	SMTP     ServerConfig `json:"smtp"`
}

func (input ConnectInput) valid() bool {
	return bounded(input.UserID, 128) && bounded(input.DisplayName, 256) && bounded(input.Username, 512) && bounded(input.Password, 4096) && input.IMAP.valid() && input.SMTP.valid()
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
