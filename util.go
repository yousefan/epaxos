package main

import "sort"

// commandsConflict determines if two commands interfere with each other
func commandsConflict(a, b Command) bool {
	// Commands conflict if they access the same key and at least one is a write
	if a.Key != b.Key {
		return false
	}

	// PUT-PUT conflicts (write-write)
	if a.Type == CmdPut && b.Type == CmdPut {
		return true
	}

	// GET-PUT conflicts (read-write)
	if (a.Type == CmdGet && b.Type == CmdPut) || (a.Type == CmdPut && b.Type == CmdGet) {
		return true
	}

	// GET-GET does not conflict (read-read)
	return false
}

// appendDependencyIfMissing adds a dependency if not already present
func appendDependencyIfMissing(deps []Dependency, replicaID, instanceID int) []Dependency {
	for _, dep := range deps {
		if dep.ReplicaID == replicaID && dep.InstanceID == instanceID {
			return deps
		}
	}
	return append(deps, Dependency{ReplicaID: replicaID, InstanceID: instanceID})
}

// equalDependencySlice compares two dependency slices for equality
func equalDependencySlice(a, b []Dependency) bool {
	if len(a) != len(b) {
		return false
	}

	// Sort both slices for comparison
	sortedA := make([]Dependency, len(a))
	sortedB := make([]Dependency, len(b))
	copy(sortedA, a)
	copy(sortedB, b)

	sort.Slice(sortedA, func(i, j int) bool {
		if sortedA[i].ReplicaID != sortedA[j].ReplicaID {
			return sortedA[i].ReplicaID < sortedA[j].ReplicaID
		}
		return sortedA[i].InstanceID < sortedA[j].InstanceID
	})

	sort.Slice(sortedB, func(i, j int) bool {
		if sortedB[i].ReplicaID != sortedB[j].ReplicaID {
			return sortedB[i].ReplicaID < sortedB[j].ReplicaID
		}
		return sortedB[i].InstanceID < sortedB[j].InstanceID
	})

	for i := range sortedA {
		if sortedA[i] != sortedB[i] {
			return false
		}
	}
	return true
}

// mergeDependencies merges dependencies from multiple replies
func mergeDependencies(replies []PreAcceptReply) []Dependency {
	depMap := make(map[Dependency]bool)

	for _, reply := range replies {
		for _, dep := range reply.Deps {
			depMap[dep] = true
		}
	}

	result := make([]Dependency, 0, len(depMap))
	for dep := range depMap {
		result = append(result, dep)
	}

	return result
}

// getMaxSeq finds the maximum sequence number from replies
func getMaxSeq(replies []PreAcceptReply) int {
	maxSeq := 0
	for _, reply := range replies {
		if reply.Seq > maxSeq {
			maxSeq = reply.Seq
		}
	}
	return maxSeq
}
