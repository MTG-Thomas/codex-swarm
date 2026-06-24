package worktree

type Spec struct {
	RepoRoot string
	Branch   string
	Path     string
}

type Manager interface {
	Create(spec Spec) error
	Remove(path string) error
}
