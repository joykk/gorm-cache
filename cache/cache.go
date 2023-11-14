package cache

import (
	"context"
	"github.com/joykk/gorm-cache/config"
	"github.com/joykk/gorm-cache/storage"
	"github.com/joykk/gorm-cache/util"
	jsoniter "github.com/json-iterator/go"
	"gorm.io/gorm"
)

var (
	_ gorm.Plugin = &Gorm2Cache{}
	_ Cache       = &Gorm2Cache{}

	json = jsoniter.Config{
		EscapeHTML:             true,
		ValidateJsonRawMessage: true,
		TagKey:                 "gormCache",
	}.Froze()
)

type Cache interface {
	Name() string
	Initialize(db *gorm.DB) error
	AttachToDB(db *gorm.DB)

	ResetCache() error
	StatsAccessor
}

type Gorm2Cache struct {
	Config     *config.CacheConfig
	Logger     util.LoggerInterface
	InstanceId string

	db       *gorm.DB
	cache    storage.DataStorage
	hitCount int64

	*stats
}

func (c *Gorm2Cache) Name() string {
	return util.GormCachePrefix
}

func (c *Gorm2Cache) Initialize(db *gorm.DB) (err error) {
	err = db.Callback().Create().After("gorm:create").Register("gorm:cache:after_create", c.AfterCreate(c))
	if err != nil {
		return err
	}

	err = db.Callback().Delete().After("gorm:delete").Register("gorm:cache:after_delete", c.AfterDelete(c))
	if err != nil {
		return err
	}

	err = db.Callback().Update().After("gorm:update").Register("gorm:cache:after_update", c.AfterUpdate(c))
	if err != nil {
		return err
	}

	err = newQueryHandler(c).Bind(db)
	if err != nil {
		return err
	}

	return
}

func (c *Gorm2Cache) AttachToDB(db *gorm.DB) {
	_ = c.Initialize(db)
}

func (c *Gorm2Cache) Init() error {
	c.InstanceId = util.GenInstanceId()

	if c.Config.CacheStorage != nil {
		c.cache = c.Config.CacheStorage
	} else {
		c.cache = storage.NewMem(storage.DefaultMemStoreConfig)
	}

	if c.Config.DebugLogger == nil {
		c.Config.DebugLogger = &util.DefaultLogger{}
	}
	c.Logger = c.Config.DebugLogger
	c.Logger.SetIsDebug(c.Config.DebugMode)

	err := c.cache.Init(&storage.Config{
		TTL:    c.Config.CacheTTL,
		Debug:  c.Config.DebugMode,
		Logger: c.Logger,
	})
	if err != nil {
		c.Logger.CtxError(context.Background(), "[Init] cache init error: %v", err)
		return err
	}
	return nil
}

func (c *Gorm2Cache) ResetCache() error {
	c.stats.ResetHitCount()
	ctx := context.Background()
	err := c.cache.CleanCache(ctx)
	if err != nil {
		c.Logger.CtxError(ctx, "[ResetCache] reset cache error: %v", err)
		return err
	}
	return nil
}

func (c *Gorm2Cache) InvalidateSearchCache(ctx context.Context, tableName string) error {
	return c.cache.DeleteKeysWithPrefix(ctx, util.GenSearchCachePrefix(c.InstanceId, tableName))
}

func (c *Gorm2Cache) InvalidatePrimaryCache(ctx context.Context, tableName string, primaryKey string) error {
	return c.cache.DeleteKey(ctx, util.GenPrimaryCacheKey(c.InstanceId, tableName, primaryKey))
}

func (c *Gorm2Cache) BatchInvalidatePrimaryCache(ctx context.Context, tableName string, primaryKeys []string) error {
	cacheKeys := make([]string, 0, len(primaryKeys))
	for _, primaryKey := range primaryKeys {
		cacheKeys = append(cacheKeys, util.GenPrimaryCacheKey(c.InstanceId, tableName, primaryKey))
	}
	return c.cache.BatchDeleteKeys(ctx, cacheKeys)
}

func (c *Gorm2Cache) InvalidateAllPrimaryCache(ctx context.Context, tableName string) error {
	return c.cache.DeleteKeysWithPrefix(ctx, util.GenPrimaryCachePrefix(c.InstanceId, tableName))
}

func (c *Gorm2Cache) BatchPrimaryKeyExists(ctx context.Context, tableName string, primaryKeys []string) (bool, error) {
	cacheKeys := make([]string, 0, len(primaryKeys))
	for _, primaryKey := range primaryKeys {
		cacheKeys = append(cacheKeys, util.GenPrimaryCacheKey(c.InstanceId, tableName, primaryKey))
	}
	return c.cache.BatchKeyExist(ctx, cacheKeys)
}

func (c *Gorm2Cache) SearchKeyExists(ctx context.Context, tableName string, SQL string, vars ...interface{}) (bool, error) {
	cacheKey := util.GenSearchCacheKey(c.InstanceId, tableName, SQL, vars...)
	return c.cache.KeyExists(ctx, cacheKey)
}

func (c *Gorm2Cache) BatchSetPrimaryKeyCache(ctx context.Context, tableName string, kvs []util.Kv) error {
	for idx, kv := range kvs {
		kvs[idx].Key = util.GenPrimaryCacheKey(c.InstanceId, tableName, kv.Key)
	}
	return c.cache.BatchSetKeys(ctx, kvs)
}

func (c *Gorm2Cache) SetSearchCache(ctx context.Context, cacheValue string, tableName string,
	sql string, vars ...interface{}) error {
	key := util.GenSearchCacheKey(c.InstanceId, tableName, sql, vars...)
	return c.cache.SetKey(ctx, util.Kv{
		Key:   key,
		Value: cacheValue,
	})
}

func (c *Gorm2Cache) GetSearchCache(ctx context.Context, tableName string, sql string, vars ...interface{}) (string, error) {
	key := util.GenSearchCacheKey(c.InstanceId, tableName, sql, vars...)
	return c.cache.GetValue(ctx, key)
}

func (c *Gorm2Cache) BatchGetPrimaryCache(ctx context.Context, tableName string, primaryKeys []string) ([]string, error) {
	cacheKeys := make([]string, 0, len(primaryKeys))
	for _, primaryKey := range primaryKeys {
		cacheKeys = append(cacheKeys, util.GenPrimaryCacheKey(c.InstanceId, tableName, primaryKey))
	}
	return c.cache.BatchGetValues(ctx, cacheKeys)
}

const InstanceCacheType = "InstanceCacheType"

// UseCache 设置本次查询使用缓存
func UseCache(db *gorm.DB) *gorm.DB {
	return db.Set(InstanceCacheType, 1)
}

// DisableCache 设置本次查询不使用缓存
func DisableCache(db *gorm.DB) *gorm.DB {
	return db.Set(InstanceCacheType, -1)
}

func (c *Gorm2Cache) ShouldCache(db *gorm.DB, tableName string) bool {
	if val, ok := db.Get(InstanceCacheType); ok {
		valInt, ok2 := val.(int)
		if ok2 {
			if valInt >= 1 {
				return true
			}
			if valInt <= -1 {
				return false
			}
		}
		// 其它cache走配置
	}
	if len(c.Config.Tables) == 0 {
		return true
	}
	if util.ContainString(tableName, c.Config.DisableTables) {
		return false
	}
	return util.ContainString(tableName, c.Config.Tables)
}
