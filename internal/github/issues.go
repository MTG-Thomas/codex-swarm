package github

import (
	"fmt"
	"strconv"
	"strings"
)

type IssueRef struct {
	Owner  string
	Repo   string
	Number int
}

func (r IssueRef) Empty() bool {
	return r.Owner == "" || r.Repo == "" || r.Number == 0
}

func (r IssueRef) String() string {
	if r.Empty() {
		return ""
	}
	return fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number)
}

func ParseIssueRef(value string) (IssueRef, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return IssueRef{}, fmt.Errorf("issue reference is required")
	}
	hash := strings.LastIndex(value, "#")
	if hash < 0 {
		return IssueRef{}, fmt.Errorf("issue reference must look like owner/repo#123")
	}
	repoPart := value[:hash]
	numberPart := value[hash+1:]
	pieces := strings.Split(repoPart, "/")
	if len(pieces) != 2 || pieces[0] == "" || pieces[1] == "" {
		return IssueRef{}, fmt.Errorf("issue reference must look like owner/repo#123")
	}
	number, err := strconv.Atoi(numberPart)
	if err != nil || number <= 0 {
		return IssueRef{}, fmt.Errorf("issue number must be a positive integer")
	}
	return IssueRef{Owner: pieces[0], Repo: pieces[1], Number: number}, nil
}
