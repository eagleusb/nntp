package nntp

import (
	"bufio"
	"bytes"
	"io"
	"log"
	"strings"
)

func checkErr(err error) bool {
	if err != nil {
		log.Printf("error : %s", err)
		return true
	}
	return false
}

func maybeId(cmd, id string) string {
	if len(id) > 0 {
		return cmd + " " + id
	}
	return cmd
}

// Read a line of bytes (up to \n) from b.
// Give up if the line exceeds maxLineLength.
// The returned bytes are a pointer into storage in
// the bufio, so they are only valid until the next bufio read.
func readLineBytes(b *bufio.Reader) (p []byte, err error) {
	if p, err = b.ReadSlice('\n'); err != nil {
		// We always know when EOF is coming.
		// If the caller asked for a line, there should be a line.
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}

	// Chop off trailing white space.
	var i int
	for i = len(p); i > 0; i-- {
		if c := p[i-1]; c != ' ' && c != '\r' && c != '\t' && c != '\n' {
			break
		}
	}
	return p[0:i], nil
}

// Read a key/value pair from b.
// A key/value has the form Key: Value\r\n
// and the Value can continue on multiple lines if each continuation line
// starts with a space/tab.
func readKeyValue(b *bufio.Reader) (key, value string, err error) {
	line, e := readLineBytes(b)
	if e == io.ErrUnexpectedEOF {
		return "", "", nil
	} else if e != nil {
		return "", "", e
	}
	if len(line) == 0 {
		return "", "", nil
	}

	// Scan first line for colon.
	i := bytes.Index(line, colon)
	if i < 0 {
		goto Malformed
	}

	key = string(line[0:i])
	if strings.Index(key, " ") >= 0 {
		// Key field has space - no good.
		goto Malformed
	}

	// Skip initial space before value.
	for i++; i < len(line); i++ {
		if line[i] != ' ' && line[i] != '\t' {
			break
		}
	}
	value = string(line[i:])

	// Look for extension lines, which must begin with space.
	for {
		c, e := b.ReadByte()
		if c != ' ' && c != '\t' {
			if e != io.EOF {
				b.UnreadByte()
			}
			break
		}

		// Eat leading space.
		for c == ' ' || c == '\t' {
			if c, e = b.ReadByte(); e != nil {
				if e == io.EOF {
					e = io.ErrUnexpectedEOF
				}
				return "", "", e
			}
		}
		b.UnreadByte()

		// Read the rest of the line and add to value.
		if line, e = readLineBytes(b); e != nil {
			return "", "", e
		}
		value += " " + string(line)
	}
	return key, value, nil

Malformed:
	return "", "", ProtocolError("malformed header line: " + string(line))
}
