package mcache

import (
	"fmt"
	"sync"
	"time"
)

// Errors for cache
const (
	ErrKeyNotFound = "Key not found"
	ErrKeyExists   = "Key already exists"
	ErrExpired     = "Key expired"
)

// CacheItem is a struct for cache item
type CacheItem struct {
	value      interface{}
	expiration int64
}

// Cache is a struct for cache
type Cache struct {
	data map[string]CacheItem
	mx   sync.RWMutex
}

// Cacher is an interface for cache
type Cacher interface {
	Set(key string, value interface{}, ttl int64) error
	Get(key string) (interface{}, error)
	Has(key string) (bool, error)
	Del(key string) error
	Cleanup()
	Clear() error
}

// NewCache is a constructor for Cache
func NewCache() *Cache {
	return &Cache{
		data: make(map[string]CacheItem),
	}
}

// Set is a method for setting key-value pair
// If key already exists, and it's not expired, return error
// If key already exists, but it's expired, set new value and return nil
// If key doesn't exist, set new value and return nil
// If ttl is 0, set value without expiration
func (c *Cache) Set(key string, value interface{}, ttl int64) error {
	c.mx.RLock()
	d, ok := c.data[key]
	c.mx.RUnlock()
	if ok {
		if d.expiration == 0 || d.expiration > time.Now().Unix() {
			return fmt.Errorf(ErrKeyExists)
		}
	}

	var expiration int64

	if ttl > 0 {
		expiration = time.Now().Unix() + ttl
	}

	c.mx.Lock()
	c.data[key] = CacheItem{
		value:      value,
		expiration: expiration,
	}
	c.mx.Unlock()
	return nil
}

// Get is a method for getting value by key
// If key doesn't exist, return error
// If key exists, but it's expired, return error and delete key
// If key exists and it's not expired, return value
func (c *Cache) Get(key string) (interface{}, error) {

	_, err := c.Has(key)
	if err != nil {
		return nil, err
	}

	// safe return?
	c.mx.RLock()
	defer c.mx.RUnlock()

	return c.data[key].value, nil
}

// Has is a method for checking if key exists.
// If key doesn't exist, return false.
// If key exists, but it's expired, return false and delete key.
// If key exists and it's not expired, return true.
func (c *Cache) Has(key string) (bool, error) {
	c.mx.RLock()
	d, ok := c.data[key]
	c.mx.RUnlock()
	if !ok {
		return false, fmt.Errorf(ErrKeyNotFound)
	}

	if d.expiration != 0 && d.expiration < time.Now().Unix() {
		c.mx.Lock()
		delete(c.data, key)
		c.mx.Unlock()
		return false, fmt.Errorf(ErrExpired)
	}

	return true, nil
}

// Del is a method for deleting key-value pair
func (c *Cache) Del(key string) error {
	_, err := c.Has(key)
	if err != nil {
		return err
	}

	c.mx.Lock()
	delete(c.data, key)
	c.mx.Unlock()
	return nil
}

// Clear is a method for clearing cache
func (c *Cache) Clear() error {
	c.mx.Lock()
	c.data = make(map[string]CacheItem)
	c.mx.Unlock()
	return nil
}

// Cleanup is a method for deleting expired keys
func (c *Cache) Cleanup() {
	c.mx.Lock()
	for k, v := range c.data {
		if v.expiration != 0 && v.expiration < time.Now().Unix() {
			delete(c.data, k)
		}
	}
	c.mx.Unlock()
}
