package antivirus

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"whisperserver/src/internal/domain"
)

// mockClamDServer starts an in-memory TCP mock server simulating ClamAV clamd protocol.
func mockClamDServer(t *testing.T, handler func(net.Conn)) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("mock clamd listen failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-done:
					return
				default:
					return
				}
			}
			go handler(conn)
		}
	}()

	cleanup := func() {
		close(done)
		_ = ln.Close()
	}
	return ln.Addr().String(), cleanup
}

func TestClamDScanner_PingAndVersion(t *testing.T) {
	addr, cleanup := mockClamDServer(t, func(conn net.Conn) {
		defer conn.Close()
		buf := make([]byte, 64)
		n, _ := conn.Read(buf)
		cmd := string(buf[:n])

		if strings.HasPrefix(cmd, "zPING") {
			_, _ = conn.Write([]byte("PONG\x00"))
		} else if strings.HasPrefix(cmd, "zVERSION") {
			_, _ = conn.Write([]byte("ClamAV 1.4.1/27400\x00"))
		}
	})
	defer cleanup()

	scanner := NewClamDScanner("tcp", addr)
	ctx := context.Background()

	if err := scanner.Ping(ctx); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}

	ver, err := scanner.Version(ctx)
	if err != nil {
		t.Fatalf("Version failed: %v", err)
	}
	if ver != "ClamAV 1.4.1/27400" {
		t.Fatalf("unexpected version: %s", ver)
	}
}

func TestClamDScanner_ScanStreamCleanAndInfected(t *testing.T) {
	addr, cleanup := mockClamDServer(t, func(conn net.Conn) {
		defer conn.Close()
		// Read command
		cmdBuf := make([]byte, 10)
		_, _ = io.ReadFull(conn, cmdBuf)

		// Read chunk stream
		var received bytes.Buffer
		lenBuf := make([]byte, 4)
		for {
			if _, err := io.ReadFull(conn, lenBuf); err != nil {
				break
			}
			chunkLen := binary.BigEndian.Uint32(lenBuf)
			if chunkLen == 0 {
				break // end of stream
			}
			chunk := make([]byte, chunkLen)
			_, _ = io.ReadFull(conn, chunk)
			received.Write(chunk)
		}

		payload := received.String()
		if strings.Contains(payload, "EICAR") {
			_, _ = conn.Write([]byte("stream: Win.Test.EICAR_HDB-1 FOUND\x00"))
		} else {
			_, _ = conn.Write([]byte("stream: OK\x00"))
		}
	})
	defer cleanup()

	scanner := NewClamDScanner("tcp", addr)
	ctx := context.Background()

	// 1. Clean payload
	cleanData := bytes.NewReader([]byte("clean audio binary data here"))
	resClean, err := scanner.ScanStream(ctx, cleanData, int64(cleanData.Len()))
	if err != nil {
		t.Fatalf("ScanStream clean failed: %v", err)
	}
	if resClean.Verdict != domain.VerdictClean {
		t.Fatalf("expected VerdictClean, got %s", resClean.Verdict)
	}

	// 2. Infected payload
	infectedData := bytes.NewReader([]byte("malicious content containing EICAR test string"))
	resInfected, err := scanner.ScanStream(ctx, infectedData, int64(infectedData.Len()))
	if err != nil {
		t.Fatalf("ScanStream infected failed: %v", err)
	}
	if resInfected.Verdict != domain.VerdictInfected {
		t.Fatalf("expected VerdictInfected, got %s", resInfected.Verdict)
	}
	if resInfected.Signature != "Win.Test.EICAR_HDB-1" {
		t.Fatalf("expected signature Win.Test.EICAR_HDB-1, got %s", resInfected.Signature)
	}
}

func TestNoopScanner(t *testing.T) {
	scanner := NewNoopScanner()
	ctx := context.Background()

	if err := scanner.Ping(ctx); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}

	data := bytes.NewReader([]byte("sample"))
	res, err := scanner.ScanStream(ctx, data, int64(data.Len()))
	if err != nil {
		t.Fatalf("ScanStream failed: %v", err)
	}
	if res.Verdict != domain.VerdictClean || res.EngineVer != "noop" {
		t.Fatalf("unexpected noop result: %+v", res)
	}
}
