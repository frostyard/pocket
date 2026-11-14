package github

import (
	"fmt"
	"regexp"
	"strconv"
)

// Ref is a pull request's canonical identity, the only source of the
// owner, name and number of a GitHub call.
type Ref struct {
	Owner, Name string
	Number      int
}

var refPattern = regexp.MustCompile(`^([A-Za-z0-9-]+)/([A-Za-z0-9._-]+)#([0-9]+)$`)

// ParseRef reads "owner/name#N", the Desk's `ref`. Anything else gets no
// GitHub call.
func ParseRef(s string) (Ref, error) {
	m := refPattern.FindStringSubmatch(s)
	if m == nil {
		return Ref{}, fmt.Errorf("not a pull request ref: %q", s)
	}
	n, err := strconv.Atoi(m[3])
	if err != nil || n <= 0 || n > 1<<31-1 {
		return Ref{}, fmt.Errorf("not a pull request number: %q", s)
	}
	return Ref{Owner: m[1], Name: m[2], Number: n}, nil
}

func (r Ref) String() string { return fmt.Sprintf("%s/%s#%d", r.Owner, r.Name, r.Number) }
