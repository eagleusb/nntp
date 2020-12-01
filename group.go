package nntp

import (
	"strconv"
	"strings"
)

// A Group gives information about a single news group on the server.
type Group struct {
	Name string
	// High and low message-numbers
	High, Low int
	// Status indicates if general posting is allowed --
	// typical values are "y", "n", or "m".
	Status string
}

// parseGroups is used to parse a list of group states.
func parseGroups(lines []string) ([]*Group, error) {
	res := make([]*Group, 0)
	for _, line := range lines {
		ss := strings.SplitN(strings.TrimSpace(line), " ", 4)
		if len(ss) < 4 {
			return nil, ProtocolError("short group info line: " + line)
		}
		high, err := strconv.Atoi(ss[1])
		if err != nil {
			return nil, ProtocolError("bad number in line: " + line)
		}
		low, err := strconv.Atoi(ss[2])
		if err != nil {
			return nil, ProtocolError("bad number in line: " + line)
		}
		res = append(res, &Group{ss[0], high, low, ss[3]})
	}
	return res, nil
}
