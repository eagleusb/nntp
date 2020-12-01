package nntp

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
)

// An Article represents an NNTP article.
type Article struct {
	Header map[string][]string
	Body   io.Reader
}

// A bodyReader satisfies reads by reading from the connection
// until it finds a line containing just .
type bodyReader struct {
	c   *Conn
	eof bool
	buf *bytes.Buffer
}

func (r *bodyReader) Read(p []byte) (n int, err error) {
	if r.eof {
		return 0, io.EOF
	}
	if r.buf == nil {
		r.buf = &bytes.Buffer{}
	}
	if r.buf.Len() == 0 {
		b, err := r.c.r.ReadBytes('\n')
		if err != nil {
			return 0, err
		}
		// canonicalize newlines
		if b[len(b)-2] == '\r' { // crlf->lf
			b = b[0 : len(b)-1]
			b[len(b)-1] = '\n'
		}
		// stop on .
		if bytes.Equal(b, dotnl) {
			r.eof = true
			return 0, io.EOF
		}
		// unescape leading ..
		if bytes.HasPrefix(b, dotdot) {
			b = b[1:]
		}
		r.buf.Write(b)
	}
	n, _ = r.buf.Read(p)
	return
}

func (r *bodyReader) discard() error {
	_, err := ioutil.ReadAll(r)
	return err
}

// articleReader satisfies reads by dumping out an article's headers
// and body.
type articleReader struct {
	a          *Article
	headerdone bool
	headerbuf  *bytes.Buffer
}

func (r *articleReader) Read(p []byte) (n int, err error) {
	if r.headerbuf == nil {
		buf := new(bytes.Buffer)
		for k, fv := range r.a.Header {
			for _, v := range fv {
				fmt.Fprintf(buf, "%s: %s\n", k, v)
			}
		}
		if r.a.Body != nil {
			fmt.Fprintf(buf, "\n")
		}
		r.headerbuf = buf
	}
	if !r.headerdone {
		n, err = r.headerbuf.Read(p)
		if err == io.EOF {
			err = nil
			r.headerdone = true
		}
		if n > 0 {
			return
		}
	}
	if r.a.Body != nil {
		n, err = r.a.Body.Read(p)
		if err == io.EOF {
			r.a.Body = nil
		}
		return
	}
	return 0, io.EOF
}

// WriteTo foo
func (a *Article) WriteTo(w io.Writer) (int64, error) {
	return io.Copy(w, &articleReader{a: a})
}

// String
func (a *Article) String() string {
	id, ok := a.Header["Message-Id"]
	if !ok {
		return "[NNTP article]"
	}
	return fmt.Sprintf("[NNTP article %s]", id[0])
}
