package util

import "errors"

var RecordNotFoundCacheHit = errors.New("record not found cache hit")
var PrimaryCacheHit = errors.New("primary cache hit")
var SearchCacheHit = errors.New("search cache hit")
var SingleFlightHit = errors.New("single flight hit")

var ErrCacheUnmarshal = errors.New("cache hit, but unmarshal error")
var ErrCacheLoadFailed = errors.New("cache hit, but load value error")

type Kv struct {
	Key   string
	Value string
}

type GetGormCachePrefixFunc func() string

// DefaultGetGormCachePrefixFunc 获取前缀名称
var DefaultGetGormCachePrefixFunc = DefaultGormCachePrefix

func DefaultGormCachePrefix() string {
	return GormCachePrefix
}

const (
	GormCachePrefix = "gormcache"
)
