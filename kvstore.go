// Updated kvstore.go with structured logging
package main

import (
	"fmt"
	"sync"
	"time"
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
	start := time.Now()

	k.mu.Lock()
	defer k.mu.Unlock()

	existed := false
	if _, exists := k.store[key]; exists {
		existed = true
	}

	k.store[key] = value
	duration := time.Since(start)

	if GetLogger() != nil {
		GetLogger().Log(DEBUG, STORAGE, "KV PUT operation completed").
			WithKV("PUT", key, value).
			WithDuration(duration).
			WithContext("key_existed", existed).
			WithContext("store_size_after", len(k.store)).
			WithTags("kvstore", "put", "success").
			Send()
	}
}

// Get retrieves a value for a given key
func (k *KVStore) Get(key string) (string, bool) {
	start := time.Now()

	k.mu.RLock()
	defer k.mu.RUnlock()

	val, ok := k.store[key]
	duration := time.Since(start)

	if GetLogger() != nil {
		if ok {
			GetLogger().Log(DEBUG, STORAGE, "KV GET operation completed").
				WithKV("GET", key, val).
				WithDuration(duration).
				WithContext("found", true).
				WithContext("store_size", len(k.store)).
				WithTags("kvstore", "get", "success").
				Send()
		} else {
			GetLogger().Log(DEBUG, STORAGE, "KV GET operation - key not found").
				WithKV("GET", key, "").
				WithDuration(duration).
				WithContext("found", false).
				WithContext("store_size", len(k.store)).
				WithError(fmt.Errorf("key not found"), "key_not_found").
				WithTags("kvstore", "get", "not_found").
				Send()
		}
	}

	return val, ok
}

// ApplyCommand applies a Command to the KV store
func (k *KVStore) ApplyCommand(cmd Command) (string, error) {
	start := time.Now()

	var result string
	var err error
	var operation string

	switch cmd.Type {
	case CmdPut:
		operation = "PUT"
		k.Put(cmd.Key, cmd.Value)
		result = cmd.Value

	case CmdGet:
		operation = "GET"
		val, ok := k.Get(cmd.Key)
		if !ok {
			err = fmt.Errorf("key not found")
			result = ""
		} else {
			result = val
		}

	default:
		operation = "UNKNOWN"
		err = fmt.Errorf("unknown command type")
		result = ""
	}

	duration := time.Since(start)

	if GetLogger() != nil {
		if err != nil {
			GetLogger().Log(WARN, STORAGE, "KV command execution failed").
				WithKV(operation, cmd.Key, cmd.Value).
				WithDuration(duration).
				WithError(err, "command_error").
				WithContext("command_type", operation).
				WithContext("result", result).
				WithTags("kvstore", "command", "failed").
				Send()
		} else {
			GetLogger().Log(DEBUG, STORAGE, "KV command executed successfully").
				WithKV(operation, cmd.Key, cmd.Value).
				WithDuration(duration).
				WithContext("command_type", operation).
				WithContext("result", result).
				WithContext("store_size", k.Size()).
				WithTags("kvstore", "command", "success").
				Send()
		}
	}

	return result, err
}

// Size returns the number of key-value pairs in the store
func (k *KVStore) Size() int {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return len(k.store)
}

// Keys returns all keys in the store
func (k *KVStore) Keys() []string {
	start := time.Now()

	k.mu.RLock()
	defer k.mu.RUnlock()

	keys := make([]string, 0, len(k.store))
	for key := range k.store {
		keys = append(keys, key)
	}

	duration := time.Since(start)

	if GetLogger() != nil {
		GetLogger().Log(DEBUG, STORAGE, "KV keys enumerated").
			WithDuration(duration).
			WithContext("key_count", len(keys)).
			WithContext("store_size", len(k.store)).
			WithTags("kvstore", "keys", "enumeration").
			Send()
	}

	return keys
}

// Clear removes all key-value pairs from the store
func (k *KVStore) Clear() {
	start := time.Now()

	k.mu.Lock()
	defer k.mu.Unlock()

	previousSize := len(k.store)
	k.store = make(map[string]string)
	duration := time.Since(start)

	if GetLogger() != nil {
		GetLogger().Log(INFO, STORAGE, "KV store cleared").
			WithDuration(duration).
			WithContext("previous_size", previousSize).
			WithContext("current_size", 0).
			WithTags("kvstore", "clear", "maintenance").
			Send()
	}
}

// Exists checks if a key exists in the store
func (k *KVStore) Exists(key string) bool {
	start := time.Now()

	k.mu.RLock()
	defer k.mu.RUnlock()

	_, exists := k.store[key]
	duration := time.Since(start)

	if GetLogger() != nil {
		GetLogger().Log(DEBUG, STORAGE, "KV key existence check").
			WithKV("EXISTS", key, "").
			WithDuration(duration).
			WithContext("exists", exists).
			WithContext("store_size", len(k.store)).
			WithTags("kvstore", "exists", "check").
			Send()
	}

	return exists
}
