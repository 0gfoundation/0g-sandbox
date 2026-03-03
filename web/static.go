package web

import _ "embed"

//go:embed dashboard.html
var DashboardHTML []byte

//go:embed ethers.umd.min.js
var EthersJS []byte
