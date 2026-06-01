package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Daytona DaytonaConfig
	Redis   RedisConfig
	Billing BillingConfig
	Chain   ChainConfig
	Server  ServerConfig
	Broker  BrokerConfig
	Alert   AlertConfig
}

type BrokerConfig struct {
	MonitorIntervalSec       int64  `mapstructure:"monitor_interval_sec"`
	TopupIntervals           int64  `mapstructure:"topup_intervals"`
	ThresholdIntervals       int64  `mapstructure:"threshold_intervals"`
	PaymentLayerURL          string `mapstructure:"payment_layer_url"`
	DepositPollIntervalSec   int64  `mapstructure:"deposit_poll_interval_sec"`
	DepositPollTimeoutSec    int64  `mapstructure:"deposit_poll_timeout_sec"`
}

type DaytonaConfig struct {
	APIURL      string `mapstructure:"api_url"`
	AdminKey    string `mapstructure:"admin_key"`
	RegistryURL string `mapstructure:"registry_url"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
}

type BillingConfig struct {
	VoucherIntervalSec  int64  `mapstructure:"voucher_interval_sec"`
	ComputePricePerSec  string `mapstructure:"compute_price_per_sec"`  // flat rate (fallback)
	PricePerCPUPerSec   string `mapstructure:"price_per_cpu_per_sec"`  // per CPU core/sec
	PricePerMemGBPerSec string `mapstructure:"price_per_mem_gb_per_sec"` // per GB memory/sec
	CreateFee           string `mapstructure:"create_fee"`
}

type ChainConfig struct {
	RPCURL          string `mapstructure:"rpc_url"`
	ContractAddress string `mapstructure:"contract_address"`
	TEEPrivateKey   string `mapstructure:"tee_private_key"`
	ProviderAddress string `mapstructure:"provider_address"`
	// AdminAddresses is the comma-separated list of wallet addresses that may
	// invoke operator-only endpoints (snapshot/registry management,
	// archive-all, force-delete, sessions). When empty, falls back to
	// [ProviderAddress] for backward compatibility with single-key
	// deployments. Distinct from ProviderAddress (the on-chain settlement
	// identity) so multiple operators can manage infrastructure without
	// holding the provider's settlement key.
	AdminAddresses string `mapstructure:"admin_addresses"`
	ChainID        int64  `mapstructure:"chain_id"`
}

// AdminList returns the parsed admin wallet addresses (lowercased hex).
// When ADMIN_ADDRESSES is unset, defaults to [ProviderAddress] so existing
// single-key deployments keep working.
func (c *ChainConfig) AdminList() []string {
	raw := strings.TrimSpace(c.AdminAddresses)
	if raw == "" {
		if c.ProviderAddress == "" {
			return nil
		}
		return []string{strings.ToLower(c.ProviderAddress)}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, strings.ToLower(p))
		}
	}
	return out
}

// IsAdmin reports whether wallet is in the admin list (case-insensitive).
func (c *ChainConfig) IsAdmin(wallet string) bool {
	if wallet == "" {
		return false
	}
	target := strings.ToLower(wallet)
	for _, a := range c.AdminList() {
		if a == target {
			return true
		}
	}
	return false
}

type ServerConfig struct {
	Port           int    `mapstructure:"port"`
	SSHGatewayHost string `mapstructure:"ssh_gateway_host"`
	BrokerURL      string `mapstructure:"broker_url"`
	// SealedOnly, when true, rejects every create-sandbox request that
	// doesn't carry `sealed: true`. Use this to run a provider that only
	// serves attested workloads (e.g. an AgenticID-hosting setup).
	SealedOnly bool `mapstructure:"sealed_only"`
}

// AlertConfig drives the operator-facing alert pipeline (settler tx failures,
// low wallet balance, voucher rejection, queue backlog). When WebhookURL is
// empty, alerts go to logs only — no external delivery.
type AlertConfig struct {
	WebhookURL              string `mapstructure:"webhook_url"`
	DedupWindowSec          int64  `mapstructure:"dedup_window_sec"`
	SettlerLowBalanceFactor int64  `mapstructure:"settler_low_balance_factor"`
	QueueBacklogThreshold   int64  `mapstructure:"queue_backlog_threshold"`
}

func Load() (*Config, error) {
	v := viper.New()

	// Defaults
	v.SetDefault("server.port", 8080)
	v.SetDefault("billing.voucher_interval_sec", 3600)
	v.SetDefault("billing.compute_price_per_sec", "16667")
	v.SetDefault("billing.price_per_cpu_per_sec", "0")
	v.SetDefault("billing.price_per_mem_gb_per_sec", "0")
	v.SetDefault("billing.create_fee", "5000000")
	v.SetDefault("redis.addr", "redis:6379")
	v.SetDefault("daytona.registry_url", "http://registry:6000")
	v.SetDefault("alert.dedup_window_sec", 3600)            // 1h between same-kind alerts
	v.SetDefault("alert.settler_low_balance_factor", 100)   // warn when balance < 100 settle-tx worth
	v.SetDefault("alert.queue_backlog_threshold", 1000)     // alert if queue depth exceeds this

	// Config file (optional)
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("/app")
	_ = v.ReadInConfig()

	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Explicit env bindings
	bindings := map[string]string{
		"daytona.api_url":              "DAYTONA_API_URL",
		"daytona.admin_key":            "DAYTONA_ADMIN_KEY",
		"daytona.registry_url":         "REGISTRY_URL",
		"redis.addr":                   "REDIS_ADDR",
		"redis.password":               "REDIS_PASSWORD",
		"billing.voucher_interval_sec": "VOUCHER_INTERVAL_SEC",
		"billing.compute_price_per_sec":   "COMPUTE_PRICE_PER_SEC",
		"billing.price_per_cpu_per_sec":   "PRICE_PER_CPU_PER_SEC",
		"billing.price_per_mem_gb_per_sec": "PRICE_PER_MEM_GB_PER_SEC",
		"billing.create_fee":               "CREATE_FEE",
		"chain.rpc_url":                "RPC_URL",
		"chain.contract_address":       "SETTLEMENT_CONTRACT",
		"chain.provider_address":       "PROVIDER_ADDRESS",
		"chain.admin_addresses":        "ADMIN_ADDRESSES",
		"chain.chain_id":               "CHAIN_ID",
		"server.port":                  "PORT",
		"server.ssh_gateway_host":       "SSH_GATEWAY_HOST",
		"server.broker_url":             "BROKER_URL",
		"server.sealed_only":            "SEALED_ONLY",
		"alert.webhook_url":                  "ALERT_WEBHOOK_URL",
		"alert.dedup_window_sec":             "ALERT_DEDUP_WINDOW_SEC",
		"alert.settler_low_balance_factor":   "SETTLER_LOW_BALANCE_FACTOR",
		"alert.queue_backlog_threshold":      "QUEUE_BACKLOG_THRESHOLD",
	}
	for key, env := range bindings {
		if err := v.BindEnv(key, env); err != nil {
			return nil, fmt.Errorf("bind env %s: %w", env, err)
		}
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	return cfg, cfg.validate()
}

// LoadBroker loads the minimal config needed by the Broker service.
// Unlike Load(), it does not require Daytona configuration.
func LoadBroker() (*Config, error) {
	v := viper.New()

	v.SetDefault("server.port", 8081)
	v.SetDefault("redis.addr", "redis:6379")

	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("/app")
	_ = v.ReadInConfig()

	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	v.SetDefault("broker.monitor_interval_sec", 300)
	v.SetDefault("broker.topup_intervals", 3)
	v.SetDefault("broker.threshold_intervals", 2)
	v.SetDefault("broker.deposit_poll_interval_sec", 5)
	v.SetDefault("broker.deposit_poll_timeout_sec", 120)

	bindings := map[string]string{
		"redis.addr":                    "REDIS_ADDR",
		"redis.password":                "REDIS_PASSWORD",
		"chain.rpc_url":                 "RPC_URL",
		"chain.contract_address":        "SETTLEMENT_CONTRACT",
		"chain.provider_address":        "PROVIDER_ADDRESS",
		"chain.chain_id":                "CHAIN_ID",
		"server.port":                   "BROKER_PORT",
		"broker.monitor_interval_sec":   "BROKER_MONITOR_INTERVAL_SEC",
		"broker.topup_intervals":        "BROKER_TOPUP_INTERVALS",
		"broker.threshold_intervals":    "BROKER_THRESHOLD_INTERVALS",
		"broker.payment_layer_url":           "PAYMENT_LAYER_URL",
		"broker.deposit_poll_interval_sec":   "BROKER_DEPOSIT_POLL_INTERVAL_SEC",
		"broker.deposit_poll_timeout_sec":    "BROKER_DEPOSIT_POLL_TIMEOUT_SEC",
	}
	for key, env := range bindings {
		if err := v.BindEnv(key, env); err != nil {
			return nil, fmt.Errorf("bind env %s: %w", env, err)
		}
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if cfg.Chain.RPCURL == "" {
		return nil, fmt.Errorf("required config missing: RPC_URL")
	}
	if cfg.Chain.ContractAddress == "" {
		return nil, fmt.Errorf("required config missing: SETTLEMENT_CONTRACT")
	}
	if cfg.Chain.ChainID == 0 {
		return nil, fmt.Errorf("required config missing: CHAIN_ID")
	}
	return cfg, nil
}

func (c *Config) validate() error {
	type req struct {
		val  string
		name string
	}
	// TEEPrivateKey is populated at startup by tee.Get() (gRPC call to the
	// tapp-daemon in a real TDX environment, or MOCK_APP_PRIVATE_KEY in mock
	// mode), so it isn't checked here.
	for _, r := range []req{
		{c.Daytona.APIURL, "DAYTONA_API_URL"},
		{c.Daytona.AdminKey, "DAYTONA_ADMIN_KEY"},
		{c.Chain.RPCURL, "RPC_URL"},
		{c.Chain.ContractAddress, "SETTLEMENT_CONTRACT"},
		{c.Chain.ProviderAddress, "PROVIDER_ADDRESS"},
	} {
		if r.val == "" {
			return fmt.Errorf("required config missing: %s", r.name)
		}
	}
	if c.Chain.ChainID == 0 {
		return fmt.Errorf("required config missing: CHAIN_ID")
	}
	return nil
}
