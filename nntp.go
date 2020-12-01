// Package nntp implements a client for the news protocol NNTP,
// as defined in RFC 3977.
package nntp

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// timeFormatNew is the NNTP time format string for NEWNEWS / NEWGROUPS
const timeFormatNew = "20060102 150405"

// timeFormatDate is the NNTP time format string for responses to the DATE command
const timeFormatDate = "20060102150405"

var dotnl  = []byte(".\n")
var dotdot = []byte("..")
var colon  = []byte{':'}

// An Error represents an error response from an NNTP server.
type Error struct {
	Code uint
	Msg  string
}

// A ProtocolError represents responses from an NNTP server
// that seem incorrect for NNTP.
type ProtocolError string

func (p ProtocolError) Error() string {
	return string(p)
}

func (e Error) Error() string {
	return fmt.Sprintf("%03d %s", e.Code, e.Msg)
}

// A Conn represents a connection to an NNTP server. The connection with
// an NNTP server is stateful; it keeps track of what group you have
// selected, if any, and (if you have a group selected) which article is
// current, next, or previous.
//
// Some methods that return information about a specific message take
// either a message-id, which is global across all NNTP servers, groups,
// and messages, or a message-number, which is an integer number that is
// local to the NNTP session and currently selected group.
//
// For all methods that return an io.Reader (or an *Article, which contains
// an io.Reader), that io.Reader is only valid until the next call to a
// method of Conn.
type Conn struct {
	conn  io.WriteCloser
	r     *bufio.Reader
	br    *bodyReader
	close bool
}

// Dial connects to an NNTP server.
// The network and addr are passed to net.Dial to
// make the connection.
func Dial(network, addr string) (*Conn, error) {
	c, err := net.Dial(network, addr)
	if checkErr(err) {
		return nil, err
	}
	return newConn(c)
}

// DialTLS connect to an NNTP server with TLS
func DialTLS(network, addr string, config *tls.Config) (*Conn, error) {
	c, err := tls.Dial(network, addr, config)
	if checkErr(err) {
		return nil, err
	}
	return newConn(c)
}

func newConn(c net.Conn) (res *Conn, err error) {
	res = &Conn{
		conn: c,
		r:    bufio.NewReaderSize(c, 4096),
	}
	if _, err = res.r.ReadString('\n'); err != nil {
		return
	}
	return
}

func (c *Conn) body() io.Reader {
	c.br = &bodyReader{c: c}
	return c.br
}

// readStrings reads a list of strings from the NNTP connection,
// stopping at a line containing only a . (Convenience method for
// LIST, etc.)
func (c *Conn) readStrings() ([]string, error) {
	var sv []string
	for {
		line, err := c.r.ReadString('\n')
		if checkErr(err) {
			return nil, err
		}
		if strings.HasSuffix(line, "\r\n") {
			line = line[0 : len(line)-2]
		} else if strings.HasSuffix(line, "\n") {
			line = line[0 : len(line)-1]
		}
		if line == "." {
			break
		}
		sv = append(sv, line)
	}
	return []string(sv), nil
}

// Authenticate logs in to the NNTP server.
// It only sends the password if the server requires one.
func (c *Conn) Authenticate(username, password string) error {
	code, _, err := c.cmd(2, "AUTHINFO USER %s", username)
	if code/100 == 3 {
		_, _, err = c.cmd(2, "AUTHINFO PASS %s", password)
	}
	return err
}

// cmd executes an NNTP command:
// It sends the command given by the format and arguments, and then
// reads the response line. If expectCode > 0, the status code on the
// response line must match it. 1 digit expectCodes only check the first
// digit of the status code, etc.
func (c *Conn) cmd(expectCode uint, format string, args ...interface{}) (code uint, line string, err error) {
	if c.close {
		return 0, "", ProtocolError("connection closed")
	}
	if c.br != nil {
		if err := c.br.discard(); err != nil {
			return 0, "", err
		}
		c.br = nil
	}
	if _, err := fmt.Fprintf(c.conn, format+"\r\n", args...); err != nil {
		return 0, "", err
	}
	line, err = c.r.ReadString('\n')
	if err != nil {
		return 0, "", err
	}
	line = strings.TrimSpace(line)
	if len(line) < 4 || line[3] != ' ' {
		return 0, "", ProtocolError("short response: " + line)
	}
	i, err := strconv.ParseUint(line[0:3], 10, 0)
	if err != nil {
		return 0, "", ProtocolError("invalid response code: " + line)
	}
	code = uint(i)
	line = line[4:]
	if 1 <= expectCode && expectCode < 10 && code/100 != expectCode ||
		10 <= expectCode && expectCode < 100 && code/10 != expectCode ||
		100 <= expectCode && expectCode < 1000 && code != expectCode {
		err = Error{code, line}
	}
	return
}

// ModeReader switches the NNTP server to "reader" mode, if it
// is a mode-switching server.
func (c *Conn) ModeReader() error {
	_, _, err := c.cmd(20, "MODE READER")
	return err
}

// NewGroups returns a list of groups added since the given time.
func (c *Conn) NewGroups(since time.Time) ([]*Group, error) {
	if _, _, err := c.cmd(231, "NEWGROUPS %s GMT", since.Format(timeFormatNew)); err != nil {
		return nil, err
	}
	return c.readGroups()
}

func (c *Conn) readGroups() ([]*Group, error) {
	lines, err := c.readStrings()
	if err != nil {
		return nil, err
	}
	return parseGroups(lines)
}

// NewNews returns a list of the IDs of articles posted
// to the given group since the given time.
func (c *Conn) NewNews(group string, since time.Time) ([]string, error) {
	if _, _, err := c.cmd(230, "NEWNEWS %s %s GMT", group, since.Format(timeFormatNew)); err != nil {
		return nil, err
	}

	id, err := c.readStrings()
	if err != nil {
		return nil, err
	}

	sort.Strings(id)
	w := 0
	for r, s := range id {
		if r == 0 || id[r-1] != s {
			id[w] = s
			w++
		}
	}
	id = id[0:w]

	return id, nil
}

// MessageOverview returned by OVER command.
type MessageOverview struct {
	MessageNumber int       // Message number in the group
	Subject       string    // Subject header value. Empty if the header is missing.
	From          string    // From header value. Empty is the header is missing.
	Date          time.Time // Parsed Date header value. Zero if the header is missing or unparseable.
	MessageId     string    // Message-Id header value. Empty is the header is missing.
	References    []string  // Message-Id's of referenced messages (References header value, split on spaces). Empty if the header is missing.
	Bytes         int       // Message size in bytes, called :bytes metadata item in RFC3977.
	Lines         int       // Message size in lines, called :lines metadata item in RFC3977.
	Extra         []string  // Any additional fields returned by the server.
}

// Overview returns overviews of all messages in the current group with message number between
// begin and end, inclusive.
func (c *Conn) Overview(begin, end int) ([]MessageOverview, error) {
	if _, _, err := c.cmd(224, "OVER %d-%d", begin, end); err != nil {
		return nil, err
	}

	lines, err := c.readStrings()
	if err != nil {
		return nil, err
	}

	result := make([]MessageOverview, 0, len(lines))
	for _, line := range lines {
		overview := MessageOverview{}
		ss := strings.SplitN(strings.TrimSpace(line), "\t", 9)
		if len(ss) < 8 {
			return nil, ProtocolError("short header listing line: " + line + strconv.Itoa(len(ss)))
		}
		overview.MessageNumber, err = strconv.Atoi(ss[0])
		if err != nil {
			return nil, ProtocolError("bad message number '" + ss[0] + "' in line: " + line)
		}
		overview.Subject = ss[1]
		overview.From = ss[2]
		overview.Date, err = parseDate(ss[3])
		if err != nil {
			// Inability to parse date is not fatal: the field in the message may be broken or missing.
			overview.Date = time.Time{}
		}
		overview.MessageId = ss[4]
		overview.References = strings.Split(ss[5], " ") // Message-Id's contain no spaces, so this is safe.
		overview.Bytes, err = strconv.Atoi(ss[6])
		if err != nil {
			return nil, ProtocolError("bad byte count '" + ss[6] + "'in line:" + line)
		}
		overview.Lines, err = strconv.Atoi(ss[7])
		if err != nil {
			return nil, ProtocolError("bad line count '" + ss[7] + "'in line:" + line)
		}
		overview.Extra = append([]string{}, ss[8:]...)
		result = append(result, overview)
	}
	return result, nil
}

// Capabilities returns a list of features this server performs.
// Not all servers support capabilities.
func (c *Conn) Capabilities() ([]string, error) {
	if _, _, err := c.cmd(101, "CAPABILITIES"); err != nil {
		return nil, err
	}
	return c.readStrings()
}

// Date returns the current time on the server.
// Typically the time is later passed to NewGroups or NewNews.
func (c *Conn) Date() (time.Time, error) {
	_, line, err := c.cmd(111, "DATE")
	if err != nil {
		return time.Time{}, err
	}
	t, err := time.Parse(timeFormatDate, line)
	if err != nil {
		return time.Time{}, ProtocolError("invalid time: " + line)
	}
	return t, nil
}

// List returns a list of groups present on the server.
// Valid forms are:
//
//   List() - return active groups
//   List(keyword) - return different kinds of information about groups
//   List(keyword, pattern) - filter groups against a glob-like pattern called a wildmat
//
func (c *Conn) List(a ...string) ([]string, error) {
	if len(a) > 2 {
		return nil, ProtocolError("List only takes up to 2 arguments")
	}
	cmd := "LIST"
	if len(a) > 0 {
		cmd += " " + a[0]
		if len(a) > 1 {
			cmd += " " + a[1]
		}
	}
	if _, _, err := c.cmd(215, cmd); err != nil {
		return nil, err
	}
	return c.readStrings()
}

// Group changes the current group.
func (c *Conn) Group(group string) (number, low, high int, err error) {
	_, line, err := c.cmd(211, "GROUP %s", group)
	if err != nil {
		return
	}

	ss := strings.SplitN(line, " ", 4) // intentional -- we ignore optional message
	if len(ss) < 3 {
		err = ProtocolError("bad group response: " + line)
		return
	}

	var n [3]int
	for i, _ := range n {
		c, e := strconv.Atoi(ss[i])
		if e != nil {
			err = ProtocolError("bad group response: " + line)
			return
		}
		n[i] = c
	}
	number, low, high = n[0], n[1], n[2]
	return
}

// Help returns the server's help text.
func (c *Conn) Help() (io.Reader, error) {
	if _, _, err := c.cmd(100, "HELP"); err != nil {
		return nil, err
	}
	return c.body(), nil
}

// nextLastStat performs the work for NEXT, LAST, and STAT.
func (c *Conn) nextLastStat(cmd, id string) (string, string, error) {
	_, line, err := c.cmd(223, maybeId(cmd, id))
	if err != nil {
		return "", "", err
	}
	ss := strings.SplitN(line, " ", 3) // optional comment ignored
	if len(ss) < 2 {
		return "", "", ProtocolError("Bad response to " + cmd + ": " + line)
	}
	return ss[0], ss[1], nil
}

// Stat looks up the message with the given id and returns its
// message number in the current group, and vice versa.
// The returned message number can be "0" if the current group
// isn't one of the groups the message was posted to.
func (c *Conn) Stat(id string) (number, msgid string, err error) {
	return c.nextLastStat("STAT", id)
}

// Last selects the previous article, returning its message number and id.
func (c *Conn) Last() (number, msgid string, err error) {
	return c.nextLastStat("LAST", "")
}

// Next selects the next article, returning its message number and id.
func (c *Conn) Next() (number, msgid string, err error) {
	return c.nextLastStat("NEXT", "")
}

// ArticleText returns the article named by id as an io.Reader.
// The article is in plain text format, not NNTP wire format.
func (c *Conn) ArticleText(id string) (io.Reader, error) {
	if _, _, err := c.cmd(220, maybeId("ARTICLE", id)); err != nil {
		return nil, err
	}
	return c.body(), nil
}

// Article returns the article named by id as an *Article.
func (c *Conn) Article(id string) (*Article, error) {
	if _, _, err := c.cmd(220, maybeId("ARTICLE", id)); err != nil {
		return nil, err
	}
	r := bufio.NewReader(c.body())
	res, err := c.readHeader(r)
	if err != nil {
		return nil, err
	}
	res.Body = r
	return res, nil
}

// HeadText returns the header for the article named by id as an io.Reader.
// The article is in plain text format, not NNTP wire format.
func (c *Conn) HeadText(id string) (io.Reader, error) {
	if _, _, err := c.cmd(221, maybeId("HEAD", id)); err != nil {
		return nil, err
	}
	return c.body(), nil
}

// Head returns the header for the article named by id as an *Article.
// The Body field in the Article is nil.
func (c *Conn) Head(id string) (*Article, error) {
	if _, _, err := c.cmd(221, maybeId("HEAD", id)); err != nil {
		return nil, err
	}
	return c.readHeader(bufio.NewReader(c.body()))
}

// Body returns the body for the article named by id as an io.Reader.
func (c *Conn) Body(id string) (io.Reader, error) {
	if _, _, err := c.cmd(222, maybeId("BODY", id)); err != nil {
		return nil, err
	}
	return c.body(), nil
}

// RawPost reads a text-formatted article from r and posts it to the server.
func (c *Conn) RawPost(r io.Reader) error {
	if _, _, err := c.cmd(3, "POST"); err != nil {
		return err
	}
	br := bufio.NewReader(r)
	eof := false
	for {
		line, err := br.ReadString('\n')
		if err == io.EOF {
			eof = true
		} else if err != nil {
			return err
		}
		if eof && len(line) == 0 {
			break
		}
		if strings.HasSuffix(line, "\n") {
			line = line[0 : len(line)-1]
		}
		var prefix string
		if strings.HasPrefix(line, ".") {
			prefix = "."
		}
		_, err = fmt.Fprintf(c.conn, "%s%s\r\n", prefix, line)
		if err != nil {
			return err
		}
		if eof {
			break
		}
	}

	if _, _, err := c.cmd(240, "."); err != nil {
		return err
	}
	return nil
}

// Post posts an article to the server.
func (c *Conn) Post(a *Article) error {
	return c.RawPost(&articleReader{a: a})
}

// Quit sends the QUIT command and closes the connection to the server.
func (c *Conn) Quit() error {
	_, _, err := c.cmd(0, "QUIT")
	c.conn.Close()
	c.close = true
	return err
}

// Internal. Parses headers in NNTP articles. Most of this is stolen from the http package,
// and it should probably be split out into a generic RFC822 header-parsing package.
func (c *Conn) readHeader(r *bufio.Reader) (res *Article, err error) {
	res = new(Article)
	res.Header = make(map[string][]string)
	for {
		var key, value string
		if key, value, err = readKeyValue(r); err != nil {
			return nil, err
		}
		if key == "" {
			break
		}
		key = http.CanonicalHeaderKey(key)
		// RFC 3977 says nothing about duplicate keys' values being equivalent to
		// a single key joined with commas, so we keep all values seperate.
		oldvalue, present := res.Header[key]
		if present {
			sv := make([]string, 0)
			sv = append(sv, oldvalue...)
			sv = append(sv, value)
			res.Header[key] = sv
		} else {
			res.Header[key] = []string{value}
		}
	}
	return res, nil
}
