package fanout

import "sort"

// group merges tasks whose paths overlap into indivisible units. A unit is
// the smallest thing a lane can be: splitting one would put two writers on
// one file, which the leases would serialize into a slower version of one
// lane.
//
// It is a union-find over tasks, joined pairwise, which is quadratic in the
// task count. A split is built once per job from a check's own findings,
// where the count is files rather than lines, so the constant that matters
// is the path comparison and not the loop.
func group(tasks []Task) []Lane {
	parent := make([]int, len(tasks))
	for i := range parent {
		parent[i] = i
	}

	find := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}

		return i
	}

	for i := range tasks {
		for j := i + 1; j < len(tasks); j++ {
			if sharePath(tasks[i], tasks[j]) {
				parent[find(i)] = find(j)
			}
		}
	}

	byRoot := map[int]*Lane{}

	var roots []int

	for i, t := range tasks {
		root := find(i)

		lane := byRoot[root]
		if lane == nil {
			lane = &Lane{}
			byRoot[root] = lane
			roots = append(roots, root)
		}

		lane.Tasks = append(lane.Tasks, t)
		lane.Weight += t.Weight
	}

	units := make([]Lane, 0, len(roots))

	for _, root := range roots {
		lane := byRoot[root]
		lane.Paths = unionPaths(lane.Tasks)
		units = append(units, *lane)
	}

	return units
}

func sharePath(a, b Task) bool {
	for _, p := range a.Paths {
		for _, q := range b.Paths {
			if overlaps(p, q) {
				return true
			}
		}
	}

	return false
}

// unionPaths is every path the lane's tasks name, with any path already
// covered by an ancestor dropped, so a lane's scope is the shortest set that
// still names all of its work.
func unionPaths(tasks []Task) []string {
	var all []string

	for _, t := range tasks {
		all = append(all, t.Paths...)
	}

	sort.Strings(all)

	out := make([]string, 0, len(all))

	for _, p := range all {
		if len(out) > 0 && overlaps(out[len(out)-1], p) {
			continue
		}

		out = append(out, p)
	}

	return out
}

// pack distributes indivisible units across at most n lanes, heaviest first
// into the lightest lane. Balancing by weight rather than by count is what
// keeps one lane holding a file with 200 findings from finishing last while
// the others idle.
func pack(units []Lane, n int) []Lane {
	sort.SliceStable(units, func(i, j int) bool { return units[i].Weight > units[j].Weight })

	if len(units) <= n {
		return units
	}

	lanes := make([]Lane, n)

	for _, u := range units {
		into := 0
		for i := range lanes {
			if lanes[i].Weight < lanes[into].Weight {
				into = i
			}
		}

		lanes[into].Tasks = append(lanes[into].Tasks, u.Tasks...)
		lanes[into].Paths = append(lanes[into].Paths, u.Paths...)
		lanes[into].Weight += u.Weight
	}

	for i := range lanes {
		sort.Strings(lanes[i].Paths)
	}

	return lanes
}
