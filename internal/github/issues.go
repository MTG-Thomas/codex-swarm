package github

type IssueRef struct {
	Owner  string
	Repo   string
	Number int
}

func (r IssueRef) Empty() bool {
	return r.Owner == "" || r.Repo == "" || r.Number == 0
}
