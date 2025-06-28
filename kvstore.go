// Updated kvstore.go
package main

import (
	"fmt"
	"sync"
)

// KVStore is a simple in-memory string-to-string map
type KVStore struct {
	mu    sync.RWMutex
	store map[string]string
}

// NewKVStore creates a new empty key-value store
func NewKVStore() *KVStore {
	return &KVStore{
		store: make(map[string]string),
	}
}

// Put sets a key to a value
func (k *KVStore) Put(key, value string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.store[key] = value

	if GetLogger() != nil {
		LogKVStoreOperation(0, "PUT", key, value, true, nil)
	}
}

// Get retrieves a value for a given key
func (k *KVStore) Get(key string) (string, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	val, ok := k.store[key]

	if GetLogger() != nil {
		var err error
		if !ok {
			err = fmt.Errorf("key not found")
			LogKVStoreOperation(0, "GET", key, "", false, err)
		} else {
			LogKVStoreOperation(0, "GET", key, val, true, nil)
		}
	}

	return val, ok
}

// ApplyCommand applies a Command to the KV store
func (k *KVStore) ApplyCommand(cmd Command) (string, error) {
	switch cmd.Type {
	case CmdPut:
		k.Put(cmd.Key, cmd.Value)
		return cmd.Value, nil // Return the stored value instead of empty string
	case CmdGet:
		val, ok := k.Get(cmd.Key)
		if !ok {
			return "", fmt.Errorf("key not found")
		}
		return val, nil
	default:
		return "", fmt.Errorf("unknown command type")
	}
}

// Size returns the number of key-value pairs in the store
func (k *KVStore) Size() int {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return len(k.store)
}

// Keys returns all keys in the store
func (k *KVStore) Keys() []string {
	k.mu.RLock()
	defer k.mu.RUnlock()

	keys := make([]string, 0, len(k.store))
	for key := range k.store {
		keys = append(keys, key)
	}
	return keys
}

// Clear removes all key-value pairs from the store
func (k *KVStore) Clear() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.store = make(map[string]string)
}

// Exists checks if a key exists in the store
func (k *KVStore) Exists(key string) bool {
	k.mu.RLock()
	defer k.mu.RUnlock()
	_, exists := k.store[key]
	return exists
}
