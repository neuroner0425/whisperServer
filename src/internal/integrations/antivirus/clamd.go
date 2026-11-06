package antivirus

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
	"whisperserver/src/internal/domain"
)

const (
	defaultDialTimeout = 5 * time.Second
	defaultChunkSize   = 32 * 1024 // 32KB
)

// ClamDScanner connects to a running ClamAV clamd daemon over TCP or Unix domain socket.
type ClamDScanner struct {
	network string
	address string
	timeout time.Duration
}

// NewClamDScanner returns a ClamAV scanner client.
// Network can be "tcp" (e.g. "localhost:3310") or "unix" (e.g. "/var/run/clamav/clamd.ctl").
func NewClamDScanner(network, address string) *ClamDScanner {
	if network == "" {
		network = "tcp"
	}
	return &ClamDScanner{
		network: network,
		address: address,
		timeout: defaultDialTimeout,
	}
}

// Ping checks if the clamd daemon is reachable and responding.
func (c *ClamDScanner) Ping(ctx context.Context) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("zPING\x00")); err != nil {
		return fmt.Errorf("clamd write PING failed: %w", err)
	}

	reader := bufio.NewReader(conn)
	resp, err := reader.ReadString('\x00')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("clamd read PING response failed: %w", err)
	}

	resp = strings.Trim(resp, "\x00\r\n ")
	if resp != "PONG" {
		return fmt.Errorf("clamd unexpected ping response: %q", resp)
	}
	return nil
}

// Version queries the engine version and virus database signature version.
func (c *ClamDScanner) Version(ctx context.Context) (string, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("zVERSION\x00")); err != nil {
		return "", fmt.Errorf("clamd write VERSION failed: %w", err)
	}

	reader := bufio.NewReader(conn)
	resp, err := reader.ReadString('\x00')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("clamd read VERSION response failed: %w", err)
	}
	return strings.Trim(resp, "\x00\r\n "), nil
}

// ScanStream streams data to clamd using the zINSTREAM chunked protocol.
func (c *ClamDScanner) ScanStream(ctx context.Context, r io.Reader, size int64) (domain.ScanResult, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return domain.ScanResult{Verdict: domain.VerdictError}, fmt.Errorf("clamd dial failed: %w", err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	}

	// 1. Send zINSTREAM command
	if _, err := conn.Write([]byte("zINSTREAM\x00")); err != nil {
		return domain.ScanResult{Verdict: domain.VerdictError}, fmt.Errorf("clamd start zINSTREAM failed: %w", err)
	}

	// 2. Stream chunks: 4-byte big-endian length prefix followed by chunk data
	buf := make([]byte, defaultChunkSize)
	lenBuf := make([]byte, 4)

	for {
		select {
		case <-ctx.Done():
			return domain.ScanResult{Verdict: domain.VerdictError}, ctx.Err()
		default:
		}

		n, readErr := r.Read(buf)
		if n > 0 {
			binary.BigEndian.PutUint32(lenBuf, uint32(n))
			if _, err := conn.Write(lenBuf); err != nil {
				return domain.ScanResult{Verdict: domain.VerdictError}, fmt.Errorf("clamd write chunk len failed: %w", err)
			}
			if _, err := conn.Write(buf[:n]); err != nil {
				return domain.ScanResult{Verdict: domain.VerdictError}, fmt.Errorf("clamd write chunk data failed: %w", err)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return domain.ScanResult{Verdict: domain.VerdictError}, fmt.Errorf("stream reader failed: %w", readErr)
		}
	}

	// 3. Terminate stream with zero-length chunk
	binary.BigEndian.PutUint32(lenBuf, 0)
	if _, err := conn.Write(lenBuf); err != nil {
		return domain.ScanResult{Verdict: domain.VerdictError}, fmt.Errorf("clamd write terminator failed: %w", err)
	}

	// 4. Read response
	reader := bufio.NewReader(conn)
	resp, err := reader.ReadBytes('\x00')
	if err != nil && !errors.Is(err, io.EOF) {
		return domain.ScanResult{Verdict: domain.VerdictError}, fmt.Errorf("clamd read scan response failed: %w", err)
	}

	cleanResp := string(bytes.Trim(resp, "\x00\r\n "))

	// Expected formats:
	// "stream: OK"
	// "stream: <Signature> FOUND"
	// "stream: <Error> ERROR"
	if cleanResp == "stream: OK" {
		return domain.ScanResult{
			Verdict:   domain.VerdictClean,
			EngineVer: "clamav",
		}, nil
	}

	if strings.HasPrefix(cleanResp, "stream: ") && strings.HasSuffix(cleanResp, " FOUND") {
		sig := strings.TrimPrefix(cleanResp, "stream: ")
		sig = strings.TrimSuffix(sig, " FOUND")
		return domain.ScanResult{
			Verdict:   domain.VerdictInfected,
			Signature: sig,
			EngineVer: "clamav",
		}, nil
	}

	return domain.ScanResult{
		Verdict:   domain.VerdictError,
		EngineVer: "clamav",
	}, fmt.Errorf("clamd scan error response: %s", cleanResp)
}

func (c *ClamDScanner) dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	d.Timeout = c.timeout
	return d.DialContext(ctx, c.network, c.address)
}


