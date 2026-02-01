package cache

// DedupCacheInterface 去重缓存接口
type DedupCacheInterface interface {
	IsSeen(address string, oid int64, direction string) bool
	Mark(address string, oid int64, direction string)
	LoadFromDB(dao interface{}) error
	Stats() map[string]interface{}
}

// PriceCacheInterface 价格缓存接口
type PriceCacheInterface interface {
	GetSpotPrice(symbol string) (float64, bool)
	SetSpotPrice(symbol string, price float64)
	GetPerpPrice(symbol string) (float64, bool)
	SetPerpPrice(symbol string, price float64)
	Stats() map[string]interface{}
}
