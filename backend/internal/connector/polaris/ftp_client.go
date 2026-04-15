package polaris

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// Upload sends filename to the Polaris FTP server using passive mode.
// Data is read from r — the file is never fully loaded into memory.
// Returns the number of bytes transferred and any error.
//
// Commands sent (exactly what Polaris supports):
//
//	USER → PASS → CWD / → TYPE I → PASV → STOR → QUIT
//
// PWD and EPSV are intentionally omitted — Polaris returns 502 for both.
func Upload(host string, port int, user, pass, filename string, r io.Reader, timeout time.Duration) (int64, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	ctrl, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return 0, fmt.Errorf("ftp: dial control %s: %w", addr, err)
	}
	defer ctrl.Close()
	ctrl.SetDeadline(time.Now().Add(timeout)) //nolint:errcheck

	// Welcome banner.
	if _, err := expectCode(ctrl, 220); err != nil {
		return 0, fmt.Errorf("ftp: welcome: %w", err)
	}

	// Auth.
	if err := sendCmd(ctrl, "USER "+user); err != nil {
		return 0, err
	}
	if _, err := expectCode(ctrl, 331); err != nil {
		return 0, fmt.Errorf("ftp: USER: %w", err)
	}
	if err := sendCmd(ctrl, "PASS "+pass); err != nil {
		return 0, err
	}
	if _, err := expectCode(ctrl, 230); err != nil {
		return 0, fmt.Errorf("ftp: PASS: %w", err)
	}

	// Change to root and set binary mode.
	if err := sendCmd(ctrl, "CWD /"); err != nil {
		return 0, err
	}
	if _, err := expectCode(ctrl, 250); err != nil {
		return 0, fmt.Errorf("ftp: CWD: %w", err)
	}
	if err := sendCmd(ctrl, "TYPE I"); err != nil {
		return 0, err
	}
	if _, err := expectCode(ctrl, 200); err != nil {
		return 0, fmt.Errorf("ftp: TYPE I: %w", err)
	}

	// Enter passive mode — parse the data address from the 227 response.
	if err := sendCmd(ctrl, "PASV"); err != nil {
		return 0, err
	}
	pasvResp, err := expectCode(ctrl, 227)
	if err != nil {
		return 0, fmt.Errorf("ftp: PASV: %w", err)
	}
	dataHost, dataPort, err := parsePASV(pasvResp)
	if err != nil {
		return 0, fmt.Errorf("ftp: parse PASV response: %w", err)
	}

	// Open data connection before sending STOR.
	dataAddr := fmt.Sprintf("%s:%d", dataHost, dataPort)
	data, err := net.DialTimeout("tcp", dataAddr, timeout)
	if err != nil {
		return 0, fmt.Errorf("ftp: dial data %s: %w", dataAddr, err)
	}
	data.SetDeadline(time.Now().Add(timeout)) //nolint:errcheck

	// Send STOR and wait for 150.
	if err := sendCmd(ctrl, "STOR "+filename); err != nil {
		data.Close()
		return 0, err
	}
	if _, err := expectCode(ctrl, 150); err != nil {
		data.Close()
		return 0, fmt.Errorf("ftp: STOR: %w", err)
	}

	// Transfer file content.
	written, err := io.Copy(data, r)
	data.Close() // closing data connection signals end-of-file to server
	if err != nil {
		return written, fmt.Errorf("ftp: data transfer: %w", err)
	}

	// Wait for 226 Transfer complete.
	if _, err := expectCode(ctrl, 226); err != nil {
		return written, fmt.Errorf("ftp: transfer complete: %w", err)
	}

	// Quit gracefully.
	sendCmd(ctrl, "QUIT") //nolint:errcheck

	return written, nil
}

// parsePASV extracts the host and port from a 227 Entering Passive Mode response.
// Format: 227 ... (h1,h2,h3,h4,p1,p2)
// Port = p1*256 + p2
func parsePASV(resp string) (string, int, error) {
	open := strings.LastIndex(resp, "(")
	close := strings.LastIndex(resp, ")")
	if open < 0 || close < 0 || close <= open {
		return "", 0, fmt.Errorf("no parenthesised address in %q", resp)
	}
	parts := strings.Split(resp[open+1:close], ",")
	if len(parts) != 6 {
		return "", 0, fmt.Errorf("expected 6 fields in PASV address, got %d: %q", len(parts), resp)
	}
	nums := make([]int, 6)
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return "", 0, fmt.Errorf("invalid PASV field %q: %w", p, err)
		}
		nums[i] = n
	}
	host := fmt.Sprintf("%d.%d.%d.%d", nums[0], nums[1], nums[2], nums[3])
	port := nums[4]*256 + nums[5]
	return host, port, nil
}

// sendCmd writes an FTP command followed by CRLF to conn.
func sendCmd(conn net.Conn, cmd string) error {
	_, err := fmt.Fprintf(conn, "%s\r\n", cmd)
	if err != nil {
		return fmt.Errorf("ftp: send %q: %w", cmd, err)
	}
	return nil
}

// expectCode reads an FTP response and returns an error if the 3-digit code
// does not match expected. Returns the full response line on success.
func expectCode(conn net.Conn, expected int) (string, error) {
	line, err := readLine(conn)
	if err != nil {
		return "", fmt.Errorf("read response (want %d): %w", expected, err)
	}
	if len(line) < 3 {
		return "", fmt.Errorf("response too short: %q", line)
	}
	code, err := strconv.Atoi(line[:3])
	if err != nil {
		return "", fmt.Errorf("non-numeric response code: %q", line)
	}
	if code != expected {
		return "", fmt.Errorf("want %d, got %d: %q", expected, code, line)
	}
	return line, nil
}

// readLine reads bytes from conn until \n and returns the trimmed line.
func readLine(conn net.Conn) (string, error) {
	var sb strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			sb.WriteByte(buf[0])
			if buf[0] == '\n' {
				return strings.TrimRight(sb.String(), "\r\n"), nil
			}
		}
		if err != nil {
			if err == io.EOF && sb.Len() > 0 {
				return strings.TrimRight(sb.String(), "\r\n"), nil
			}
			return "", err
		}
	}
}
