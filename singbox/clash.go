package singbox

import "github.com/sleroq/sb/clash"

// Compatibility aliases for callers using the original sing-box API client.
// The wire API is also used by mihomo; configuration formats are not shared.
type Clash = clash.Client
type Selector = clash.Selector
