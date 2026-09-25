package proxies

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

const (
	socksVersion       = 5
	socksNoAuth        = 0
	socksUserPassword  = 2
	socksNoMethod      = 255
	socksConnect       = 1
	socksAddressIPv4   = 1
	socksAddressDomain = 3
	socksAddressIPv6   = 4
)

func (m *Manager) serveSOCKS5(runtime *listenerRuntime) {
	for {
		connection, err := runtime.listener.Accept()
		if err != nil {
			m.report(fmt.Errorf("serve socks5 proxy on %s: %w", runtime.config.address(), err))
			return
		}
		go serveSOCKSConnection(connection, runtime.config)
	}
}

func serveSOCKSConnection(client net.Conn, configured Config) {
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReader(client)
	if err := socksHandshake(reader, client, configured); err != nil {
		return
	}
	target, err := socksRequest(reader, client)
	if err != nil {
		return
	}
	dialer := newDestinationDialer(configured.AllowPrivate)
	upstream, err := dialer.DialContext(context.Background(), "tcp", target)
	if err != nil {
		_, _ = client.Write([]byte{socksVersion, 4, 0, socksAddressIPv4, 0, 0, 0, 0, 0, 0})
		return
	}
	defer upstream.Close()
	_ = client.SetDeadline(time.Time{})
	if _, err := client.Write([]byte{socksVersion, 0, 0, socksAddressIPv4, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	tunnel(client, upstream)
}

func socksHandshake(reader *bufio.Reader, writer io.Writer, configured Config) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil || header[0] != socksVersion || header[1] == 0 {
		return fmt.Errorf("invalid socks5 greeting")
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return err
	}
	wanted := byte(socksNoAuth)
	if configured.hasAuthentication() {
		wanted = socksUserPassword
	}
	accepted := false
	for _, method := range methods {
		if method == wanted {
			accepted = true
			break
		}
	}
	if !accepted {
		_, _ = writer.Write([]byte{socksVersion, socksNoMethod})
		return fmt.Errorf("socks5 authentication method unavailable")
	}
	if _, err := writer.Write([]byte{socksVersion, wanted}); err != nil {
		return err
	}
	if wanted == socksUserPassword {
		return socksAuthenticate(reader, writer, configured)
	}
	return nil
}

func socksAuthenticate(reader *bufio.Reader, writer io.Writer, configured Config) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil || header[0] != 1 {
		return fmt.Errorf("invalid socks5 authentication request")
	}
	username := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, username); err != nil {
		return err
	}
	length, err := reader.ReadByte()
	if err != nil {
		return err
	}
	password := make([]byte, int(length))
	if _, err := io.ReadFull(reader, password); err != nil {
		return err
	}
	usernameHash := sha256.Sum256(username)
	configuredUsernameHash := sha256.Sum256([]byte(configured.Username))
	passwordHash := sha256.Sum256(password)
	configuredPasswordHash := sha256.Sum256([]byte(configured.Password))
	userMatch := subtle.ConstantTimeCompare(usernameHash[:], configuredUsernameHash[:])
	passwordMatch := subtle.ConstantTimeCompare(passwordHash[:], configuredPasswordHash[:])
	if userMatch&passwordMatch != 1 {
		_, _ = writer.Write([]byte{1, 1})
		return fmt.Errorf("socks5 authentication failed")
	}
	_, err = writer.Write([]byte{1, 0})
	return err
}

func socksRequest(reader *bufio.Reader, writer io.Writer) (string, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return "", err
	}
	if header[0] != socksVersion || header[1] != socksConnect {
		_, _ = writer.Write([]byte{socksVersion, 7, 0, socksAddressIPv4, 0, 0, 0, 0, 0, 0})
		return "", fmt.Errorf("socks5 supports CONNECT only")
	}
	var host string
	switch header[3] {
	case socksAddressIPv4:
		address := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(reader, address); err != nil {
			return "", err
		}
		host = net.IP(address).String()
	case socksAddressIPv6:
		address := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(reader, address); err != nil {
			return "", err
		}
		host = net.IP(address).String()
	case socksAddressDomain:
		length, err := reader.ReadByte()
		if err != nil || length == 0 {
			return "", fmt.Errorf("invalid socks5 domain")
		}
		address := make([]byte, int(length))
		if _, err := io.ReadFull(reader, address); err != nil {
			return "", err
		}
		host = string(address)
	default:
		_, _ = writer.Write([]byte{socksVersion, 8, 0, socksAddressIPv4, 0, 0, 0, 0, 0, 0})
		return "", fmt.Errorf("unsupported socks5 address type")
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(reader, portBytes); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(portBytes)
	if port == 0 {
		return "", fmt.Errorf("invalid socks5 destination port")
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}
