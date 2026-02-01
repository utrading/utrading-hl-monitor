package hyperliquid

var mainnetAliasMap = map[string]string{
	"UBTC":   "BTC",
	"UETH":   "ETH",
	"USOL":   "SOL",
	"UFART":  "FARTCOIN",
	"USDT0":  "USDT",
	"HPENGU": "PENGU",
	"XAUT0":  "XAUT",
	"UPUMP":  "PUMP",
	"PUMP":   "PUMP-26",
	"UUUSPX": "SPX",
	"UBONK":  "BONK",
	"WMNT":   "MNT",
	"UXPL":   "XPL",
	"UDZ":    "2Z",
}

var testnetAliasMap = map[string]string{
	"UNIT":  "BTC",
	"UETH":  "ETH",
	"USOL":  "SOL",
	"UFART": "FARTCOIN",
	"TZERO": "USDT",
	"UPUMP": "PUMP",
	"USPXS": "SPX",
	"UXPL":  "XPL",
	"UDZ":   "2Z",
}

func MainnetToAlias(name string) string {
	if v, ok := mainnetAliasMap[name]; ok {
		return v
	}

	return name
}

func TestnetToAlias(name string) string {
	if v, ok := testnetAliasMap[name]; ok {
		return v
	}

	return name
}
