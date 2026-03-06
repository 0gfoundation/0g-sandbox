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
}

type DaytonaConfig struct {
	APIURL   string `mapstructure:"api_url"`
	AdminKey string `mapstructure:"admin_key"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
}

type BillingConfig struct {
	VoucherIntervalSec int64  `mapstructure:"voucher_interval_sec"`
	ComputePricePerSec string `mapstructure:"compute_price_per_sec"`
	CreateFee          string `mapstructure:"create_fee"`
}

type ChainConfig struct {
	RPCURL             string `mapstructure:"rpc_url"`
	ContractAddress    string `mapstructure:"contract_address"`
	TEEPrivateKey      string `mapstructure:"tee_private_key"`
	ProviderPrivateKey string `mapstructure:"provider_private_key"`
	ProviderAddress    string `mapstructure:"provider_address"`
	ChainID            int64  `mapstructure:"chain_id"`
}

type ServerConfig struct {
	Port int `mapstructure:"port"`
}

func Load() (*Config, error) {
	v := viper.New()

	// Defaults
	v.SetDefault("server.port", 8080)
	v.SetDefault("billing.voucher_interval_sec", 3600)
	v.SetDefault("billing.compute_price_per_sec", "16667")
	v.SetDefault("billing.create_fee", "5000000")
	v.SetDefault("redis.addr", "redis:6379")

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
		"redis.addr":                   "REDIS_ADDR",
		"redis.password":               "REDIS_PASSWORD",
		"billing.voucher_interval_sec": "VOUCHER_INTERVAL_SEC",
		"billing.compute_price_per_sec": "COMPUTE_PRICE_PER_SEC",
		"billing.create_fee":           "CREATE_FEE",
		"chain.rpc_url":                "RPC_URL",
		"chain.contract_address":       "SETTLEMENT_CONTRACT",
		"chain.provider_private_key":   "PROVIDER_PRIVATE_KEY",
		"chain.provider_address":       "PROVIDER_ADDRESS",
		"chain.chain_id":               "CHAIN_ID",
		"server.port":                  "PORT",
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

func (c *Config) validate() error {
	type req struct {
		val  string
		name string
	}
	// TEEPrivateKey and PROVIDER_ADDRESS are optional here: if absent they
	// are populated at startup by tee.Get() (gRPC call to the tapp-daemon in
	// a real TDX environment, or MOCK_APP_PRIVATE_KEY in mock mode).
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
	return nil
}
