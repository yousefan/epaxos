package main

func commandsConflict(a, b Command) bool {
	// Only PUT commands to the same key conflict
	if a.Type == CmdPut && b.Type == CmdPut && a.Key == b.Key {
		return true
	}
	return false
}

func appendIfMissing(slice []int, val int) []int {
	for _, v := range slice {
		if v == val {
			return slice
		}
	}
	return append(slice, val)
}

func equalIntSlice(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[int]int)
	for _, v := range a {
		m[v]++
	}
	for _, v := range b {
		if m[v] == 0 {
			return false
		}
		m[v]--
	}
	return true
}

func mergeDeps(rs []struct {
	Seq  int
	Deps []int
}) []int {
	m := make(map[int]bool)
	for _, r := range rs {
		for _, d := range r.Deps {
			m[d] = true
		}
	}
	result := []int{}
	for d := range m {
		result = append(result, d)
	}
	return result
}

func GetReplicaIDFromAddress(address string) ReplicaID {
	// Implementation depends on your address format
	// This is a placeholder
	return 0
}
