package chain

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

// In-process JSON-RPC exercises the actual generated bindings and block
// selectors without a listener, an EVM deployment, or an external chain.
type sealIssuerRPC struct {
	chainID      *big.Int
	registry     common.Address
	owner        common.Address
	appID        string
	epoch        *big.Int
	bindingEpoch *big.Int
	bindingHash  [32]byte
	active       bool
	reorg        bool
	failMethod   string
	stallMethod  string
	gate         chan struct{}
	blocks       []string
	sandboxABI   abi.ABI
	tappABI      abi.ABI
}

func (s *sealIssuerRPC) ChainId(context.Context) (*hexutil.Big, error) {
	return (*hexutil.Big)(s.chainID), nil
}

func (s *sealIssuerRPC) GetBlockByNumber(_ context.Context, number string, _ bool) (*types.Header, error) {
	h := &types.Header{Number: big.NewInt(101), Difficulty: big.NewInt(1), GasLimit: 30000000, Time: 1234}
	if number != "latest" && s.reorg {
		h.Time++
	}
	return h, nil
}

func (s *sealIssuerRPC) Call(_ context.Context, args map[string]any, block string) (hexutil.Bytes, error) {
	s.blocks = append(s.blocks, block)
	data, _ := args["input"].(string)
	if data == "" {
		data, _ = args["data"].(string)
	}
	raw, err := hexutil.Decode(data)
	if err != nil {
		return nil, err
	}
	m, err := s.sandboxABI.MethodById(raw[:4])
	if err != nil {
		m, err = s.tappABI.MethodById(raw[:4])
	}
	if err != nil {
		return nil, err
	}
	if m.Name == s.failMethod {
		return nil, errors.New("simulated RPC failure")
	}
	if m.Name == s.stallMethod {
		<-s.gate
	}
	switch m.Name {
	case "tappRegistry":
		return m.Outputs.Pack(s.registry)
	case "getAppInfo":
		return m.Outputs.Pack(TappRegistryAppInfo{Owner: s.owner, RegisteredAt: s.epoch})
	case "services":
		return m.Outputs.Pack("https://sandbox.test", s.appID, big.NewInt(1), big.NewInt(1), big.NewInt(1))
	case "kmsBindings":
		return m.Outputs.Pack(s.bindingEpoch, s.bindingHash)
	case "isActiveProvider":
		return m.Outputs.Pack(s.active)
	default:
		return nil, errors.New("unexpected contract method")
	}
}

func newSealIssuerRPC(t *testing.T) (*Client, *sealIssuerRPC) {
	t.Helper()
	sabi, err := abi.JSON(strings.NewReader(SandboxServingABI))
	if err != nil {
		t.Fatal(err)
	}
	tabi, err := abi.JSON(strings.NewReader(TappRegistryABI))
	if err != nil {
		t.Fatal(err)
	}
	s := &sealIssuerRPC{
		chainID: big.NewInt(16602), registry: common.HexToAddress("0x1111111111111111111111111111111111111111"),
		owner: common.HexToAddress("0x2222222222222222222222222222222222222222"), appID: "sandbox-test",
		epoch: big.NewInt(1234), bindingEpoch: big.NewInt(1234), bindingHash: crypto.Keccak256Hash([]byte("sandbox-test")), active: true,
		sandboxABI: sabi, tappABI: tabi, gate: make(chan struct{}),
	}
	server := rpc.NewServer()
	if err := server.RegisterName("eth", s); err != nil {
		t.Fatal(err)
	}
	eth := ethclient.NewClient(rpc.DialInProc(server))
	t.Cleanup(func() { close(s.gate); eth.Close(); server.Stop() })
	contract, err := NewSandboxServing(common.HexToAddress("0x3333333333333333333333333333333333333333"), eth)
	if err != nil {
		t.Fatal(err)
	}
	tapp, err := NewTappRegistry(s.registry, eth)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := crypto.GenerateKey()
	return &Client{eth: eth, contract: contract, tapp: tapp, tappAddr: s.registry, chainID: big.NewInt(16602), kmsProvider: true,
		providerKey: key, providerAddr: crypto.PubkeyToAddress(key.PublicKey)}, s
}

func TestGetSealedAppInstanceFixedBlock(t *testing.T) {
	c, s := newSealIssuerRPC(t)
	epoch, err := c.GetSealedAppInstance(context.Background(), "sandbox-test")
	if err != nil {
		t.Fatal(err)
	}
	if epoch != 1234 || len(s.blocks) != 5 {
		t.Fatalf("epoch %d, contract reads %v", epoch, s.blocks)
	}
	for _, block := range s.blocks {
		if block != "0x65" {
			t.Fatalf("contract read at %s instead of fixed block 101", block)
		}
	}
}

func TestGetSealedAppInstanceRejectsWrongScope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Client, *sealIssuerRPC)
	}{
		{"node_key_mode", func(c *Client, _ *sealIssuerRPC) { c.kmsProvider = false }},
		{"rpc_chain", func(_ *Client, s *sealIssuerRPC) { s.chainID = big.NewInt(1) }},
		{"registry", func(_ *Client, s *sealIssuerRPC) { s.registry[0]++ }},
		{"owner_zero", func(_ *Client, s *sealIssuerRPC) { s.owner = common.Address{} }},
		{"epoch_zero", func(_ *Client, s *sealIssuerRPC) { s.epoch = big.NewInt(0) }},
		{"epoch_overflow", func(_ *Client, s *sealIssuerRPC) { s.epoch = new(big.Int).Lsh(big.NewInt(1), 64) }},
		{"service_app", func(_ *Client, s *sealIssuerRPC) { s.appID += " " }},
		{"binding_epoch", func(_ *Client, s *sealIssuerRPC) { s.bindingEpoch = big.NewInt(1233) }},
		{"binding_app", func(_ *Client, s *sealIssuerRPC) { s.bindingHash[0]++ }},
		{"inactive", func(_ *Client, s *sealIssuerRPC) { s.active = false }},
		{"reorg", func(_ *Client, s *sealIssuerRPC) { s.reorg = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, s := newSealIssuerRPC(t)
			tc.change(c, s)
			if _, err := c.GetSealedAppInstance(context.Background(), "sandbox-test"); err == nil {
				t.Fatal("accepted invalid issuer")
			}
		})
	}
}

func TestGetSealedAppInstanceRPCFailureIsNotNodeFallback(t *testing.T) {
	for _, method := range []string{"tappRegistry", "getAppInfo", "services", "kmsBindings", "isActiveProvider"} {
		t.Run(method, func(t *testing.T) {
			c, s := newSealIssuerRPC(t)
			s.failMethod = method
			if _, err := c.GetSealedAppInstance(context.Background(), "sandbox-test"); err == nil {
				t.Fatal("RPC failure accepted")
			}
		})
	}
}

func TestGetSealedAppInstanceBoundsStalledRPC(t *testing.T) {
	c, s := newSealIssuerRPC(t)
	s.stallMethod = "kmsBindings"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.GetSealedAppInstance(ctx, "sandbox-test"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("RPC did not honor the shared deadline")
	}
}
