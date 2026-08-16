package models

const unknownEnumString = "unknown"

// VCSType identifies the version control system managing a repo.
type VCSType int

// VCSType values.
const (
	VCSTypeGit VCSType = iota
	VCSTypeJJ
)

func (v VCSType) String() string {
	switch v {
	case VCSTypeGit:
		return "git"
	case VCSTypeJJ:
		return "jj"
	default:
		return unknownEnumString
	}
}

// FilterMode identifies a repo list filter criterion.
type FilterMode int

// FilterMode values.
const (
	FilterModeAll FilterMode = iota
	FilterModeAhead
	FilterModeBehind
	FilterModeDirty
	FilterModeHasPR
	FilterModeHasStash
	FilterModeHasNotes
)

func (f FilterMode) String() string {
	switch f {
	case FilterModeAll:
		return "All"
	case FilterModeAhead:
		return "Ahead"
	case FilterModeBehind:
		return "Behind"
	case FilterModeDirty:
		return "Dirty"
	case FilterModeHasPR:
		return "Has PR"
	case FilterModeHasStash:
		return "Has Stash"
	case FilterModeHasNotes:
		return "Has Notes"
	default:
		return "Unknown"
	}
}

// ShortKey returns the single-character key binding label for the filter mode.
func (f FilterMode) ShortKey() string {
	switch f {
	case FilterModeAll:
		return "a"
	case FilterModeAhead:
		return ">"
	case FilterModeBehind:
		return "<"
	case FilterModeDirty:
		return "d"
	case FilterModeHasPR:
		return "p"
	case FilterModeHasStash:
		return "s"
	case FilterModeHasNotes:
		return "n"
	default:
		return "?"
	}
}

// AllFilterModes returns every FilterMode in display order.
func AllFilterModes() []FilterMode {
	return []FilterMode{
		FilterModeAll,
		FilterModeAhead,
		FilterModeBehind,
		FilterModeDirty,
		FilterModeHasPR,
		FilterModeHasStash,
		FilterModeHasNotes,
	}
}

// SelectableFilterModes returns the FilterModes a user can toggle, excluding FilterModeAll.
func SelectableFilterModes() []FilterMode {
	return []FilterMode{
		FilterModeDirty,
		FilterModeAhead,
		FilterModeBehind,
		FilterModeHasPR,
		FilterModeHasStash,
		FilterModeHasNotes,
	}
}

// SortMode identifies a repo list sort criterion.
type SortMode int

// SortMode values.
const (
	SortModeName SortMode = iota
	SortModeModified
	SortModeStatus
	SortModeBranch
)

// sortModeCount is the number of SortMode values, used to cycle modes.
const sortModeCount = 4

func (s SortMode) String() string {
	switch s {
	case SortModeName:
		return "Name"
	case SortModeModified:
		return "Modified"
	case SortModeStatus:
		return "Status"
	case SortModeBranch:
		return "Branch"
	default:
		return "Unknown"
	}
}

// ShortKey returns the single-character key binding label for the sort mode.
func (s SortMode) ShortKey() string {
	switch s {
	case SortModeName:
		return "n"
	case SortModeModified:
		return "m"
	case SortModeStatus:
		return "s"
	case SortModeBranch:
		return "b"
	default:
		return "?"
	}
}

// Next returns the next SortMode in cyclic order.
func (s SortMode) Next() SortMode {
	return SortMode((int(s) + 1) % sortModeCount)
}

// AllSortModes returns every SortMode in display order.
func AllSortModes() []SortMode {
	return []SortMode{
		SortModeName,
		SortModeModified,
		SortModeStatus,
		SortModeBranch,
	}
}

// RepoStatus summarizes a repo's overall working tree and sync state.
type RepoStatus int

// RepoStatus values.
const (
	RepoStatusClean RepoStatus = iota
	RepoStatusDirty
	RepoStatusAhead
	RepoStatusBehind
	RepoStatusDiverged
)

func (r RepoStatus) String() string {
	switch r {
	case RepoStatusClean:
		return "clean"
	case RepoStatusDirty:
		return "dirty"
	case RepoStatusAhead:
		return "ahead"
	case RepoStatusBehind:
		return "behind"
	case RepoStatusDiverged:
		return "diverged"
	default:
		return unknownEnumString
	}
}

// ItemKind identifies the kind of item shown in a repo's detail tabs.
type ItemKind int

// ItemKind values.
const (
	ItemKindBranch ItemKind = iota
	ItemKindStash
	ItemKindWorktree
)

func (i ItemKind) String() string {
	switch i {
	case ItemKindBranch:
		return "branch"
	case ItemKindStash:
		return "stash"
	case ItemKindWorktree:
		return "worktree"
	default:
		return unknownEnumString
	}
}
