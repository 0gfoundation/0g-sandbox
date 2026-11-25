package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
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
	Usage   UsageConfig
}

type BrokerConfig struct {
	MonitorIntervalSec     int64  `mapstructure:"monitor_interval_sec"`
	TopupIntervals         int64  `mapstructure:"topup_intervals"`
	ThresholdIntervals     int64  `mapstructure:"threshold_intervals"`
	PaymentLayerURL        string `mapstructure:"payment_layer_url"`
	DepositPollIntervalSec int64  `mapstructure:"deposit_poll_interval_sec"`
	DepositPollTimeoutSec  int64  `mapstructure:"deposit_poll_timeout_sec"`
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
	VoucherIntervalSec int64 `mapstructure:"voucher_interval_sec"`
	// SettleIntervalSec decouples SETTLEMENT cadence from ACCOUNTING cadence.
	// VoucherIntervalSec alone drove both: a voucher was emitted every interval
	// and the settler, blocked in BLPOP, submitted it the moment it landed — so
	// the queue never accumulated and every voucher cost its own transaction
	// (measured on dev: ~0.00098 0G gas to collect a 0.004 0G voucher, ~25%).
	// With this set, the settler waits before draining, so the existing batching
	// (maxBatchSize, and the contract's per-voucher loop across users) actually
	// has something to batch. 0 = fall back to VoucherIntervalSec (today's
	// behaviour). Accounting granularity is unaffected — vouchers are still
	// emitted every VoucherIntervalSec.
	SettleIntervalSec   int64  `mapstructure:"settle_interval_sec"`
	ComputePricePerSec  string `mapstructure:"compute_price_per_sec"`    // flat rate (fallback)
	PricePerCPUPerSec   string `mapstructure:"price_per_cpu_per_sec"`    // per CPU core/sec
	PricePerMemGBPerSec string `mapstructure:"price_per_mem_gb_per_sec"` // per GB memory/sec
	CreateFee           string `mapstructure:"create_fee"`
}

type ChainConfig struct {
	RPCURL          string `mapstructure:"rpc_url"`
	ContractAddress string `mapstructure:"contract_address"`
	TappRegistry    string `mapstructure:"tapp_registry"`
	TEEPrivateKey   string `mapstructure:"tee_private_key"`
	// ProviderKeySource picks the provider identity (PROVIDER_KEY_SOURCE):
	// "local" (default) is the node's TEE signer, which tapp makes anew on
	// every tapp-server restart; "kms" is the app's KMS key
	// (tee.GetProviderKey), the same on every boot and node.
	ProviderKeySource string `mapstructure:"provider_key_source"`
	// ProviderPrivateKey is set at startup from tee.GetProviderKey when
	// ProviderKeySource is "kms"; empty means the provider is the TEE signer.
	ProviderPrivateKey string `mapstructure:"-"`
	// AdminAddresses is an ADDITIVE comma-separated list of extra wallet
	// addresses that may invoke operator-only endpoints (snapshot/registry
	// management, archive-all, force-delete, sessions). The appId's
	// TappRegistry owner is ALWAYS an admin on top of this list — it is
	// resolved from the chain at runtime (getAppInfo(BACKEND_APP_NAME).owner),
	// never configured, so it can't drift from the on-chain truth.
	AdminAddresses string `mapstructure:"admin_addresses"`
	// AttestorAppID authorizes the discovered stable app signer and, with a
	// KMS provider, serves the attestor's TLS pins at GET /api/attestor-tls.
	// Operator wallets in AdminAddresses remain separate from application
	// identities.
	AttestorAppID string `mapstructure:"attestor_app_id"`
	// AttestorInstance (ATTESTOR_INSTANCE, "<registeredAt>:<owner>") is the
	// operator's explicit authorization of the attestor app's registration
	// instance. It replaces the instance the provider stored on its first
	// verified lookup; Load normalizes it to the stored form. Empty keeps the
	// stored instance.
	AttestorInstance string `mapstructure:"attestor_instance"`
	ChainID          int64  `mapstructure:"chain_id"`
}

// AdminList returns the parsed extra admin wallet addresses (lowercased hex).
// The appId owner is NOT in this list — it is resolved from the chain at
// runtime and is always an admin on top of these.
func (c *ChainConfig) AdminList() []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.Split(c.AdminAddresses, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

type ServerConfig struct {
	TLSEnabled     bool   `mapstructure:"tls_enabled"`
	Port           int    `mapstructure:"port"`
	SSHGatewayHost string `mapstructure:"ssh_gateway_host"`
	BrokerURL      string `mapstructure:"broker_url"`
	// SealedOnly, when true, rejects every create-sandbox request that
	// doesn't carry `sealed: true`. Use this to run a provider that only
	// serves attested workloads (e.g. an AgenticID-hosting setup).
	SealedOnly bool `mapstructure:"sealed_only"`

	// AuthStrict requires every signed request to carry the provider address
	// and (on :id routes) the resource id. Leave off only while legacy clients
	// that predate provider binding are still in circulation — an omitted
	// binding is exactly what a replayed legacy capture looks like.
	AuthStrict bool `mapstructure:"auth_strict"`
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

// UsageConfig drives per-sandbox usage monitoring (#148). Empty RunnerAPIURL
// disables it: no usage reports, alerts or disk enforcement.
type UsageConfig struct {
	RunnerAPIURL      string  `mapstructure:"runner_api_url"`
	RunnerAPIKey      string  `mapstructure:"runner_api_key"`
	PollSec           int64   `mapstructure:"poll_sec"`
	DiskEnforce       bool    `mapstructure:"disk_enforce"`
	DiskWarnFreeMiB   int64   `mapstructure:"disk_warn_free_mib"`
	DiskGraceSec      int64   `mapstructure:"disk_grace_sec"`
	VolumeAlertPct    float64 `mapstructure:"volume_alert_pct"`
	VolumeCriticalPct float64 `mapstructure:"volume_critical_pct"`
	// VolumeCreateBlockPct refuses new sandboxes while the shared volume is at
	// least this full; 0 never refuses.
	VolumeCreateBlockPct float64 `mapstructure:"volume_create_block_pct"`
	OOMKillsPerHour      int     `mapstructure:"oom_kills_per_hour"`
	CPUPinnedHours       float64 `mapstructure:"cpu_pinned_hours"`
	// Notices sends each running sandbox the provider's warnings through the
	// runner (internal/notice).
	Notices bool `mapstructure:"notices"`
	// BalanceNoticeMin warns an owner's sandboxes when the balance runs out
	// within this many minutes; 0 never warns.
	BalanceNoticeMin int64 `mapstructure:"balance_notice_min"`
}

func Load() (*Config, error) {
	v := viper.New()

	// Defaults
	v.SetDefault("server.port", 8080)
	v.SetDefault("billing.voucher_interval_sec", 3600)
	v.SetDefault("billing.settle_interval_sec", 0) // 0 = same as voucher_interval_sec
	v.SetDefault("billing.compute_price_per_sec", "16667")
	v.SetDefault("billing.price_per_cpu_per_sec", "0")
	v.SetDefault("billing.price_per_mem_gb_per_sec", "0")
	v.SetDefault("billing.create_fee", "5000000")
	v.SetDefault("chain.provider_key_source", "local")
	v.SetDefault("redis.addr", "redis:6379")
	v.SetDefault("daytona.registry_url", "http://registry:6000")
	v.SetDefault("alert.dedup_window_sec", 3600)          // 1h between same-kind alerts
	v.SetDefault("alert.settler_low_balance_factor", 100) // warn when balance < 100 settle-tx worth
	v.SetDefault("alert.queue_backlog_threshold", 1000)   // alert if queue depth exceeds this
	v.SetDefault("usage.poll_sec", 60)
	v.SetDefault("usage.disk_enforce", false) // alert and report only until turned on
	v.SetDefault("usage.disk_warn_free_mib", 1024)
	v.SetDefault("usage.disk_grace_sec", 900)
	v.SetDefault("usage.volume_alert_pct", 80)
	v.SetDefault("usage.volume_critical_pct", 95)
	v.SetDefault("usage.volume_create_block_pct", 90)
	v.SetDefault("usage.oom_kills_per_hour", 3)
	v.SetDefault("usage.cpu_pinned_hours", 6)
	v.SetDefault("usage.notices", true)
	v.SetDefault("usage.balance_notice_min", 60)

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
		"daytona.api_url":                  "DAYTONA_API_URL",
		"daytona.admin_key":                "DAYTONA_ADMIN_KEY",
		"daytona.registry_url":             "REGISTRY_URL",
		"redis.addr":                       "REDIS_ADDR",
		"redis.password":                   "REDIS_PASSWORD",
		"billing.voucher_interval_sec":     "VOUCHER_INTERVAL_SEC",
		"billing.settle_interval_sec":      "SETTLE_INTERVAL_SEC",
		"billing.compute_price_per_sec":    "COMPUTE_PRICE_PER_SEC",
		"billing.price_per_cpu_per_sec":    "PRICE_PER_CPU_PER_SEC",
		"billing.price_per_mem_gb_per_sec": "PRICE_PER_MEM_GB_PER_SEC",
		"billing.create_fee":               "CREATE_FEE",
		"chain.rpc_url":                    "RPC_URL",
		"chain.contract_address":           "SETTLEMENT_CONTRACT",
		"chain.tapp_registry":              "TAPP_REGISTRY",
		"chain.admin_addresses":            "ADMIN_ADDRESSES",
		"chain.attestor_app_id":            "ATTESTOR_APP_ID",
		"chain.attestor_instance":          "ATTESTOR_INSTANCE",
		"chain.chain_id":                   "CHAIN_ID",
		"chain.provider_key_source":        "PROVIDER_KEY_SOURCE",
		"server.port":                      "PORT",
		"server.tls_enabled":               "TLS_ENABLED",
		"server.ssh_gateway_host":          "SSH_GATEWAY_HOST",
		"server.broker_url":                "BROKER_URL",
		"server.sealed_only":               "SEALED_ONLY",
		"server.auth_strict":               "AUTH_STRICT",
		"alert.webhook_url":                "ALERT_WEBHOOK_URL",
		"alert.dedup_window_sec":           "ALERT_DEDUP_WINDOW_SEC",
		"alert.settler_low_balance_factor": "SETTLER_LOW_BALANCE_FACTOR",
		"alert.queue_backlog_threshold":    "QUEUE_BACKLOG_THRESHOLD",
		"usage.runner_api_url":             "RUNNER_API_URL",
		"usage.runner_api_key":             "RUNNER_API_KEY",
		"usage.poll_sec":                   "USAGE_POLL_SEC",
		"usage.disk_enforce":               "DISK_ENFORCE",
		"usage.disk_warn_free_mib":         "DISK_WARN_FREE_MIB",
		"usage.disk_grace_sec":             "DISK_GRACE_SEC",
		"usage.volume_alert_pct":           "VOLUME_ALERT_PCT",
		"usage.volume_critical_pct":        "VOLUME_CRITICAL_PCT",
		"usage.volume_create_block_pct":    "VOLUME_CREATE_BLOCK_PCT",
		"usage.oom_kills_per_hour":         "OOM_ALERT_KILLS_PER_HOUR",
		"usage.cpu_pinned_hours":           "CPU_PINNED_ALERT_HOURS",
		"usage.notices":                    "SANDBOX_NOTICES",
		"usage.balance_notice_min":         "BALANCE_NOTICE_MIN",
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
		"redis.addr":                       "REDIS_ADDR",
		"redis.password":                   "REDIS_PASSWORD",
		"chain.rpc_url":                    "RPC_URL",
		"chain.contract_address":           "SETTLEMENT_CONTRACT",
		"chain.tapp_registry":              "TAPP_REGISTRY",
		"chain.chain_id":                   "CHAIN_ID",
		"server.port":                      "BROKER_PORT",
		"broker.monitor_interval_sec":      "BROKER_MONITOR_INTERVAL_SEC",
		"broker.topup_intervals":           "BROKER_TOPUP_INTERVALS",
		"broker.threshold_intervals":       "BROKER_THRESHOLD_INTERVALS",
		"broker.payment_layer_url":         "PAYMENT_LAYER_URL",
		"broker.deposit_poll_interval_sec": "BROKER_DEPOSIT_POLL_INTERVAL_SEC",
		"broker.deposit_poll_timeout_sec":  "BROKER_DEPOSIT_POLL_TIMEOUT_SEC",
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
	if cfg.Chain.TappRegistry == "" {
		return nil, fmt.Errorf("required config missing: TAPP_REGISTRY")
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
	} {
		if r.val == "" {
			return fmt.Errorf("required config missing: %s", r.name)
		}
	}
	if c.Chain.ChainID == 0 {
		return fmt.Errorf("required config missing: CHAIN_ID")
	}
	if c.Usage.RunnerAPIURL != "" && c.Usage.RunnerAPIKey == "" {
		return fmt.Errorf("RUNNER_API_KEY is required when RUNNER_API_URL is set")
	}
	for _, p := range []struct {
		env string
		v   float64
	}{
		{"VOLUME_ALERT_PCT", c.Usage.VolumeAlertPct},
		{"VOLUME_CRITICAL_PCT", c.Usage.VolumeCriticalPct},
		{"VOLUME_CREATE_BLOCK_PCT", c.Usage.VolumeCreateBlockPct},
	} {
		if p.v < 0 || p.v > 100 {
			return fmt.Errorf("%s must be between 0 and 100, got %v", p.env, p.v)
		}
	}
	if c.Usage.BalanceNoticeMin < 0 {
		return fmt.Errorf("BALANCE_NOTICE_MIN must not be negative, got %d", c.Usage.BalanceNoticeMin)
	}
	if c.Chain.ProviderKeySource != "local" && c.Chain.ProviderKeySource != "kms" {
		return fmt.Errorf("PROVIDER_KEY_SOURCE must be \"local\" or \"kms\", got %q", c.Chain.ProviderKeySource)
	}
	if c.Chain.AttestorInstance != "" {
		instance, err := NormalizeAttestorInstance(c.Chain.AttestorInstance)
		if err != nil {
			return err
		}
		c.Chain.AttestorInstance = instance
	}
	return nil
}

// NormalizeAttestorInstance parses "<registeredAt>:<owner>" (the attestor
// app's getAppInfo registeredAt in decimal and its owner address) into the
// form the provider stores: the decimal epoch, a colon and the lowercase
// 0x-prefixed owner.
func NormalizeAttestorInstance(value string) (string, error) {
	epoch, owner, ok := strings.Cut(strings.TrimSpace(value), ":")
	registeredAt, err := strconv.ParseUint(epoch, 10, 64)
	if !ok || err != nil || registeredAt == 0 || !strings.HasPrefix(owner, "0x") ||
		!common.IsHexAddress(owner) || common.HexToAddress(owner) == (common.Address{}) {
		return "", fmt.Errorf("ATTESTOR_INSTANCE must be \"<registeredAt>:<0x owner>\" from the attestor app's getAppInfo, got %q", value)
	}
	return strconv.FormatUint(registeredAt, 10) + ":" + strings.ToLower(common.HexToAddress(owner).Hex()), nil
}

// Warnings lists settings that start the server with a feature unavailable.
func (c *Config) Warnings() []string {
	var out []string
	if c.Chain.AttestorAppID != "" && c.Chain.ProviderKeySource != "kms" {
		out = append(out, "ATTESTOR_APP_ID is set without PROVIDER_KEY_SOURCE=kms: GET /api/attestor-tls answers 503")
	}
	if c.Chain.AttestorAppID != "" && !c.Server.TLSEnabled {
		out = append(out, "ATTESTOR_APP_ID is set without TLS_ENABLED=true: callers cannot pin this provider's attested app TLS")
	}
	if c.Chain.AttestorInstance != "" && c.Chain.AttestorAppID == "" {
		out = append(out, "ATTESTOR_INSTANCE is set without ATTESTOR_APP_ID: it has no effect")
	}
	return out
}
