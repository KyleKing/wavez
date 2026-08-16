package models

// ActiveFilter is a filter mode's current enabled/inverted state.
type ActiveFilter struct {
	Mode     FilterMode
	Enabled  bool
	Inverted bool
}

// DisplayName returns the filter mode's display label.
func (f ActiveFilter) DisplayName() string {
	return f.Mode.String()
}

// ShortKey returns the filter mode's key binding label.
func (f ActiveFilter) ShortKey() string {
	return f.Mode.ShortKey()
}

// NewActiveFilter builds an ActiveFilter for mode, enabled by default only for FilterModeAll.
func NewActiveFilter(mode FilterMode) ActiveFilter {
	return ActiveFilter{
		Mode:     mode,
		Enabled:  mode == FilterModeAll,
		Inverted: false,
	}
}

// SortDirection is the direction a sort mode is applied in.
type SortDirection int

// SortDirection values.
const (
	SortDirectionOff SortDirection = iota
	SortDirectionAsc
	SortDirectionDesc
)

func (d SortDirection) String() string {
	switch d {
	case SortDirectionAsc:
		return "ASC"
	case SortDirectionDesc:
		return "DESC"
	default:
		return ""
	}
}

// ActiveSort is a sort mode's current direction and priority among active sorts.
type ActiveSort struct {
	Mode      SortMode
	Direction SortDirection
	Priority  int
}

// DisplayName returns the sort mode's display label, including its direction if active.
func (s ActiveSort) DisplayName() string {
	name := s.Mode.String()
	if s.Direction != SortDirectionOff {
		name += " (" + s.Direction.String() + ")"
	}

	return name
}

// ShortKey returns the sort mode's key binding label.
func (s ActiveSort) ShortKey() string {
	return s.Mode.ShortKey()
}

// IsEnabled reports whether the sort is currently applied.
func (s ActiveSort) IsEnabled() bool {
	return s.Direction != SortDirectionOff
}

// NewActiveSort builds an ActiveSort for mode with direction off at the given priority.
func NewActiveSort(mode SortMode, priority int) ActiveSort {
	return ActiveSort{
		Mode:      mode,
		Direction: SortDirectionOff,
		Priority:  priority,
	}
}
