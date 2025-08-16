package main

import (
	"sort"
	"time"
)

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

func (r *Replica) onCommitted(rid, iid int) {
	r.InstanceLock.Lock()
	defer r.InstanceLock.Unlock()

	inst := r.Instances[rid][iid]
	if inst == nil {
		return
	}

	// Remove from uncommitted index since it's now committed (aggressive cleanup)
	r.indexRemoveFromCommitted(inst.Command, rid, iid)

	// Build reverse edges once
	key := makeKey(rid, iid)
	if _, ok := r.remainingDeps[key]; !ok {
		// count how many deps are not yet executed (with age limit)
		unexec := 0
		currentTime := time.Now()
		for _, d := range inst.Deps {
			// Skip old dependencies
			if depMap, ok := r.Instances[d.ReplicaID]; ok {
				if depInst, ok := depMap[d.InstanceID]; ok {
					// Only count recent dependencies
					if !depInst.Timestamp.Time.Before(currentTime.Add(-time.Duration(r.maxDependencyAge) * time.Millisecond)) {
						dkey := makeKey(d.ReplicaID, d.InstanceID)
						// register reverse edge: d -> (rid,iid)
						r.dependents[dkey] = append(r.dependents[dkey], instKey{rid, iid})

						if !depInst.Executed {
							unexec++
						}
					}
				}
			} else {
				// Instance doesn't exist yet
				dkey := makeKey(d.ReplicaID, d.InstanceID)
				r.dependents[dkey] = append(r.dependents[dkey], instKey{rid, iid})
				unexec++
			}
		}
		r.remainingDeps[key] = unexec
	}

	// If all deps already executed (or too old to matter), mark runnable
	if r.remainingDeps[key] == 0 {
		r.enqueueReadyLocked(instKey{rid, iid})
	}
}

func (r *Replica) enqueueReadyLocked(k instKey) {
	key := makeKey(k.rid, k.iid)
	if _, seen := r.pending[key]; seen {
		return
	}
	r.pending[key] = struct{}{}
	select {
	case r.readyCh <- k:
	default:
		// queue full: fall back once (non-blocking drop) or log & use blocking send
		r.readyCh <- k
	}
}

func (r *Replica) onExecuted(rid, iid int) {
	r.InstanceLock.Lock()
	defer r.InstanceLock.Unlock()

	key := makeKey(rid, iid)
	for _, dep := range r.dependents[key] {
		dkey := makeKey(dep.rid, dep.iid)
		if cnt, ok := r.remainingDeps[dkey]; ok && cnt > 0 {
			cnt--
			r.remainingDeps[dkey] = cnt
			if cnt == 0 {
				r.enqueueReadyLocked(dep)
			}
		}
	}
	// Optional: free memory
	delete(r.dependents, key)
}
