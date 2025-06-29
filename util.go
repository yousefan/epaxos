package main

import (
	"sort"
)

func commandsConflict(a, b Command) bool {
	// PUT-PUT conflicts: two writes to the same key
	if a.Type == CmdPut && b.Type == CmdPut && a.Key == b.Key {
		return true
	}

	// GET-PUT conflicts: read and write to the same key (for linearizability)
	if (a.Type == CmdGet && b.Type == CmdPut && a.Key == b.Key) ||
		(a.Type == CmdPut && b.Type == CmdGet && a.Key == b.Key) {
		return true
	}

	// PUT-GET conflicts: write and read to the same key (for linearizability)
	// This ensures that a read sees the most recent write
	if (a.Type == CmdPut && b.Type == CmdGet && a.Key == b.Key) ||
		(a.Type == CmdGet && b.Type == CmdPut && a.Key == b.Key) {
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

func appendIfMissingCrossReplica(slice []CrossReplicaDependency, val CrossReplicaDependency) []CrossReplicaDependency {
	for _, v := range slice {
		if v.ReplicaID == val.ReplicaID && v.InstanceID == val.InstanceID {
			return slice
		}
	}
	return append(slice, val)
}

func equalIntSlice(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}

	// Sort both slices for comparison
	sortedA := make([]int, len(a))
	sortedB := make([]int, len(b))
	copy(sortedA, a)
	copy(sortedB, b)
	sort.Ints(sortedA)
	sort.Ints(sortedB)

	for i := range sortedA {
		if sortedA[i] != sortedB[i] {
			return false
		}
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
	sort.Ints(result) // Sort for deterministic ordering
	return result
}

// GetReplicaIDFromAddress extracts replica ID from address
func GetReplicaIDFromAddress(address string) ReplicaID {
	// Implementation depends on your address format
	// This is a placeholder - should parse address to get replica ID
	return 0
}

// CrossReplicaDependency represents a dependency on an instance from another replica
type CrossReplicaDependency struct {
	ReplicaID  ReplicaID
	InstanceID int
}

// DependencySet represents a set of cross-replica dependencies
type DependencySet struct {
	Deps []CrossReplicaDependency
}

// AddDependency adds a dependency if not already present
func (ds *DependencySet) AddDependency(replicaID ReplicaID, instanceID int) {
	dep := CrossReplicaDependency{ReplicaID: replicaID, InstanceID: instanceID}
	for _, existing := range ds.Deps {
		if existing.ReplicaID == dep.ReplicaID && existing.InstanceID == dep.InstanceID {
			return // Already exists
		}
	}
	ds.Deps = append(ds.Deps, dep)
}

// Contains checks if a dependency exists
func (ds *DependencySet) Contains(replicaID ReplicaID, instanceID int) bool {
	for _, dep := range ds.Deps {
		if dep.ReplicaID == replicaID && dep.InstanceID == instanceID {
			return true
		}
	}
	return false
}

// Merge merges another dependency set into this one
func (ds *DependencySet) Merge(other *DependencySet) {
	for _, dep := range other.Deps {
		ds.AddDependency(dep.ReplicaID, dep.InstanceID)
	}
}

// ToLocalDeps converts cross-replica dependencies to local instance IDs
// This is used when all dependencies are from the same replica
func (ds *DependencySet) ToLocalDeps(replicaID ReplicaID) []int {
	var localDeps []int
	for _, dep := range ds.Deps {
		if dep.ReplicaID == replicaID {
			localDeps = append(localDeps, dep.InstanceID)
		}
	}
	sort.Ints(localDeps)
	return localDeps
}

// equalCrossReplicaDeps compares two slices of cross-replica dependencies
func equalCrossReplicaDeps(a, b []CrossReplicaDependency) bool {
	if len(a) != len(b) {
		return false
	}

	// Sort both slices for comparison
	sortedA := make([]CrossReplicaDependency, len(a))
	sortedB := make([]CrossReplicaDependency, len(b))
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
		if sortedA[i].ReplicaID != sortedB[i].ReplicaID ||
			sortedA[i].InstanceID != sortedB[i].InstanceID {
			return false
		}
	}
	return true
}

// mergeCrossReplicaDeps merges cross-replica dependencies from multiple sources
func mergeCrossReplicaDeps(rs []struct {
	Seq  int
	Deps []CrossReplicaDependency
}) []CrossReplicaDependency {
	m := make(map[CrossReplicaDependency]bool)
	for _, r := range rs {
		for _, d := range r.Deps {
			m[d] = true
		}
	}
	result := []CrossReplicaDependency{}
	for d := range m {
		result = append(result, d)
	}

	// Sort for deterministic ordering
	sort.Slice(result, func(i, j int) bool {
		if result[i].ReplicaID != result[j].ReplicaID {
			return result[i].ReplicaID < result[j].ReplicaID
		}
		return result[i].InstanceID < result[j].InstanceID
	})

	return result
}
