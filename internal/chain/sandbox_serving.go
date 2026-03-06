// Code generated - DO NOT EDIT.
// This file is a generated binding and any manual changes will be lost.

package chain

import (
	"errors"
	"math/big"
	"strings"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
)

// Reference imports to suppress errors if they are not otherwise used.
var (
	_ = errors.New
	_ = big.NewInt
	_ = strings.NewReader
	_ = ethereum.NotFound
	_ = bind.Bind
	_ = common.Big1
	_ = types.BloomLookup
	_ = event.NewSubscription
	_ = abi.ConvertType
)

// SandboxServingSandboxVoucher is an auto generated low-level Go binding around an user-defined struct.
type SandboxServingSandboxVoucher struct {
	User      common.Address
	Provider  common.Address
	TotalFee  *big.Int
	UsageHash [32]byte
	Nonce     *big.Int
	Signature []byte
}

// SandboxServingMetaData contains all meta data concerning the SandboxServing contract.
var SandboxServingMetaData = &bind.MetaData{
	ABI: "[{\"type\":\"constructor\",\"inputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"LOCK_TIME\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"acknowledgeTEESigner\",\"inputs\":[{\"name\":\"provider\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"acknowledged\",\"type\":\"bool\",\"internalType\":\"bool\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"addOrUpdateService\",\"inputs\":[{\"name\":\"url\",\"type\":\"string\",\"internalType\":\"string\"},{\"name\":\"teeSignerAddress\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"computePricePerMin\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"createFee\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"outputs\":[],\"stateMutability\":\"payable\"},{\"type\":\"function\",\"name\":\"balanceOfBatch\",\"inputs\":[{\"name\":\"users\",\"type\":\"address[]\",\"internalType\":\"address[]\"},{\"name\":\"provider\",\"type\":\"address\",\"internalType\":\"address\"}],\"outputs\":[{\"name\":\"balances\",\"type\":\"uint256[]\",\"internalType\":\"uint256[]\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"deposit\",\"inputs\":[{\"name\":\"recipient\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"provider\",\"type\":\"address\",\"internalType\":\"address\"}],\"outputs\":[],\"stateMutability\":\"payable\"},{\"type\":\"function\",\"name\":\"domainSeparator\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"getBalance\",\"inputs\":[{\"name\":\"user\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"provider\",\"type\":\"address\",\"internalType\":\"address\"}],\"outputs\":[{\"name\":\"balance\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"pendingRefund\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"refundUnlockAt\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"getLastNonce\",\"inputs\":[{\"name\":\"user\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"provider\",\"type\":\"address\",\"internalType\":\"address\"}],\"outputs\":[{\"name\":\"\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"getProviderEarnings\",\"inputs\":[{\"name\":\"provider\",\"type\":\"address\",\"internalType\":\"address\"}],\"outputs\":[{\"name\":\"\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"initialize\",\"inputs\":[{\"name\":\"providerStake_\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"isTEEAcknowledged\",\"inputs\":[{\"name\":\"user\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"provider\",\"type\":\"address\",\"internalType\":\"address\"}],\"outputs\":[{\"name\":\"\",\"type\":\"bool\",\"internalType\":\"bool\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"owner\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"address\",\"internalType\":\"address\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"previewSettlementResults\",\"inputs\":[{\"name\":\"vouchers\",\"type\":\"tuple[]\",\"internalType\":\"structSandboxServing.SandboxVoucher[]\",\"components\":[{\"name\":\"user\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"provider\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"totalFee\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"usageHash\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"},{\"name\":\"nonce\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"signature\",\"type\":\"bytes\",\"internalType\":\"bytes\"}]}],\"outputs\":[{\"name\":\"statuses\",\"type\":\"uint8[]\",\"internalType\":\"enumSandboxServing.SettlementStatus[]\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"providerEarnings\",\"inputs\":[{\"name\":\"\",\"type\":\"address\",\"internalType\":\"address\"}],\"outputs\":[{\"name\":\"\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"providerStake\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"providerStakes\",\"inputs\":[{\"name\":\"\",\"type\":\"address\",\"internalType\":\"address\"}],\"outputs\":[{\"name\":\"\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"requestRefund\",\"inputs\":[{\"name\":\"provider\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"amount\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"serviceExists\",\"inputs\":[{\"name\":\"\",\"type\":\"address\",\"internalType\":\"address\"}],\"outputs\":[{\"name\":\"\",\"type\":\"bool\",\"internalType\":\"bool\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"services\",\"inputs\":[{\"name\":\"\",\"type\":\"address\",\"internalType\":\"address\"}],\"outputs\":[{\"name\":\"url\",\"type\":\"string\",\"internalType\":\"string\"},{\"name\":\"teeSignerAddress\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"computePricePerMin\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"createFee\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"signerVersion\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"setProviderStake\",\"inputs\":[{\"name\":\"newStake\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"settleFeesWithTEE\",\"inputs\":[{\"name\":\"vouchers\",\"type\":\"tuple[]\",\"internalType\":\"structSandboxServing.SandboxVoucher[]\",\"components\":[{\"name\":\"user\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"provider\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"totalFee\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"usageHash\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"},{\"name\":\"nonce\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"signature\",\"type\":\"bytes\",\"internalType\":\"bytes\"}]}],\"outputs\":[{\"name\":\"statuses\",\"type\":\"uint8[]\",\"internalType\":\"enumSandboxServing.SettlementStatus[]\"}],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"transferOwnership\",\"inputs\":[{\"name\":\"newOwner\",\"type\":\"address\",\"internalType\":\"address\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"withdrawEarnings\",\"inputs\":[],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"withdrawRefund\",\"inputs\":[{\"name\":\"provider\",\"type\":\"address\",\"internalType\":\"address\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"event\",\"name\":\"Deposited\",\"inputs\":[{\"name\":\"recipient\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"provider\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"sender\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"amount\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"EarningsWithdrawn\",\"inputs\":[{\"name\":\"provider\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"amount\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"OwnershipTransferred\",\"inputs\":[{\"name\":\"previousOwner\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"newOwner\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"ProviderStakeUpdated\",\"inputs\":[{\"name\":\"oldStake\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"},{\"name\":\"newStake\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"RefundRequested\",\"inputs\":[{\"name\":\"user\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"provider\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"amount\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"},{\"name\":\"unlockAt\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"RefundWithdrawn\",\"inputs\":[{\"name\":\"user\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"provider\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"amount\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"ServiceUpdated\",\"inputs\":[{\"name\":\"provider\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"url\",\"type\":\"string\",\"indexed\":false,\"internalType\":\"string\"},{\"name\":\"teeSignerAddress\",\"type\":\"address\",\"indexed\":false,\"internalType\":\"address\"},{\"name\":\"signerVersion\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"TEESignerAcknowledged\",\"inputs\":[{\"name\":\"user\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"provider\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"acknowledged\",\"type\":\"bool\",\"indexed\":false,\"internalType\":\"bool\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"VoucherSettled\",\"inputs\":[{\"name\":\"user\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"provider\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"totalFee\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"},{\"name\":\"usageHash\",\"type\":\"bytes32\",\"indexed\":false,\"internalType\":\"bytes32\"},{\"name\":\"nonce\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"},{\"name\":\"status\",\"type\":\"uint8\",\"indexed\":false,\"internalType\":\"enumSandboxServing.SettlementStatus\"}],\"anonymous\":false}]",
}

// SandboxServingABI is the input ABI used to generate the binding from.
// Deprecated: Use SandboxServingMetaData.ABI instead.
var SandboxServingABI = SandboxServingMetaData.ABI

// SandboxServing is an auto generated Go binding around an Ethereum contract.
type SandboxServing struct {
	SandboxServingCaller     // Read-only binding to the contract
	SandboxServingTransactor // Write-only binding to the contract
	SandboxServingFilterer   // Log filterer for contract events
}

// SandboxServingCaller is an auto generated read-only Go binding around an Ethereum contract.
type SandboxServingCaller struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// SandboxServingTransactor is an auto generated write-only Go binding around an Ethereum contract.
type SandboxServingTransactor struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// SandboxServingFilterer is an auto generated log filtering Go binding around an Ethereum contract events.
type SandboxServingFilterer struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// SandboxServingSession is an auto generated Go binding around an Ethereum contract,
// with pre-set call and transact options.
type SandboxServingSession struct {
	Contract     *SandboxServing   // Generic contract binding to set the session for
	CallOpts     bind.CallOpts     // Call options to use throughout this session
	TransactOpts bind.TransactOpts // Transaction auth options to use throughout this session
}

// SandboxServingCallerSession is an auto generated read-only Go binding around an Ethereum contract,
// with pre-set call options.
type SandboxServingCallerSession struct {
	Contract *SandboxServingCaller // Generic contract caller binding to set the session for
	CallOpts bind.CallOpts         // Call options to use throughout this session
}

// SandboxServingTransactorSession is an auto generated write-only Go binding around an Ethereum contract,
// with pre-set transact options.
type SandboxServingTransactorSession struct {
	Contract     *SandboxServingTransactor // Generic contract transactor binding to set the session for
	TransactOpts bind.TransactOpts         // Transaction auth options to use throughout this session
}

// SandboxServingRaw is an auto generated low-level Go binding around an Ethereum contract.
type SandboxServingRaw struct {
	Contract *SandboxServing // Generic contract binding to access the raw methods on
}

// SandboxServingCallerRaw is an auto generated low-level read-only Go binding around an Ethereum contract.
type SandboxServingCallerRaw struct {
	Contract *SandboxServingCaller // Generic read-only contract binding to access the raw methods on
}

// SandboxServingTransactorRaw is an auto generated low-level write-only Go binding around an Ethereum contract.
type SandboxServingTransactorRaw struct {
	Contract *SandboxServingTransactor // Generic write-only contract binding to access the raw methods on
}

// NewSandboxServing creates a new instance of SandboxServing, bound to a specific deployed contract.
func NewSandboxServing(address common.Address, backend bind.ContractBackend) (*SandboxServing, error) {
	contract, err := bindSandboxServing(address, backend, backend, backend)
	if err != nil {
		return nil, err
	}
	return &SandboxServing{SandboxServingCaller: SandboxServingCaller{contract: contract}, SandboxServingTransactor: SandboxServingTransactor{contract: contract}, SandboxServingFilterer: SandboxServingFilterer{contract: contract}}, nil
}

// NewSandboxServingCaller creates a new read-only instance of SandboxServing, bound to a specific deployed contract.
func NewSandboxServingCaller(address common.Address, caller bind.ContractCaller) (*SandboxServingCaller, error) {
	contract, err := bindSandboxServing(address, caller, nil, nil)
	if err != nil {
		return nil, err
	}
	return &SandboxServingCaller{contract: contract}, nil
}

// NewSandboxServingTransactor creates a new write-only instance of SandboxServing, bound to a specific deployed contract.
func NewSandboxServingTransactor(address common.Address, transactor bind.ContractTransactor) (*SandboxServingTransactor, error) {
	contract, err := bindSandboxServing(address, nil, transactor, nil)
	if err != nil {
		return nil, err
	}
	return &SandboxServingTransactor{contract: contract}, nil
}

// NewSandboxServingFilterer creates a new log filterer instance of SandboxServing, bound to a specific deployed contract.
func NewSandboxServingFilterer(address common.Address, filterer bind.ContractFilterer) (*SandboxServingFilterer, error) {
	contract, err := bindSandboxServing(address, nil, nil, filterer)
	if err != nil {
		return nil, err
	}
	return &SandboxServingFilterer{contract: contract}, nil
}

// bindSandboxServing binds a generic wrapper to an already deployed contract.
func bindSandboxServing(address common.Address, caller bind.ContractCaller, transactor bind.ContractTransactor, filterer bind.ContractFilterer) (*bind.BoundContract, error) {
	parsed, err := SandboxServingMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	return bind.NewBoundContract(address, *parsed, caller, transactor, filterer), nil
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_SandboxServing *SandboxServingRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _SandboxServing.Contract.SandboxServingCaller.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_SandboxServing *SandboxServingRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _SandboxServing.Contract.SandboxServingTransactor.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_SandboxServing *SandboxServingRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _SandboxServing.Contract.SandboxServingTransactor.contract.Transact(opts, method, params...)
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_SandboxServing *SandboxServingCallerRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _SandboxServing.Contract.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_SandboxServing *SandboxServingTransactorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _SandboxServing.Contract.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_SandboxServing *SandboxServingTransactorRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _SandboxServing.Contract.contract.Transact(opts, method, params...)
}

// LOCKTIME is a free data retrieval call binding the contract method 0x413d9c3a.
//
// Solidity: function LOCK_TIME() view returns(uint256)
func (_SandboxServing *SandboxServingCaller) LOCKTIME(opts *bind.CallOpts) (*big.Int, error) {
	var out []interface{}
	err := _SandboxServing.contract.Call(opts, &out, "LOCK_TIME")

	if err != nil {
		return *new(*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	return out0, err

}

// LOCKTIME is a free data retrieval call binding the contract method 0x413d9c3a.
//
// Solidity: function LOCK_TIME() view returns(uint256)
func (_SandboxServing *SandboxServingSession) LOCKTIME() (*big.Int, error) {
	return _SandboxServing.Contract.LOCKTIME(&_SandboxServing.CallOpts)
}

// LOCKTIME is a free data retrieval call binding the contract method 0x413d9c3a.
//
// Solidity: function LOCK_TIME() view returns(uint256)
func (_SandboxServing *SandboxServingCallerSession) LOCKTIME() (*big.Int, error) {
	return _SandboxServing.Contract.LOCKTIME(&_SandboxServing.CallOpts)
}

// BalanceOfBatch is a free data retrieval call binding the contract method 0x8a921690.
//
// Solidity: function balanceOfBatch(address[] users, address provider) view returns(uint256[] balances)
func (_SandboxServing *SandboxServingCaller) BalanceOfBatch(opts *bind.CallOpts, users []common.Address, provider common.Address) ([]*big.Int, error) {
	var out []interface{}
	err := _SandboxServing.contract.Call(opts, &out, "balanceOfBatch", users, provider)

	if err != nil {
		return *new([]*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new([]*big.Int)).(*[]*big.Int)

	return out0, err

}

// BalanceOfBatch is a free data retrieval call binding the contract method 0x8a921690.
//
// Solidity: function balanceOfBatch(address[] users, address provider) view returns(uint256[] balances)
func (_SandboxServing *SandboxServingSession) BalanceOfBatch(users []common.Address, provider common.Address) ([]*big.Int, error) {
	return _SandboxServing.Contract.BalanceOfBatch(&_SandboxServing.CallOpts, users, provider)
}

// BalanceOfBatch is a free data retrieval call binding the contract method 0x8a921690.
//
// Solidity: function balanceOfBatch(address[] users, address provider) view returns(uint256[] balances)
func (_SandboxServing *SandboxServingCallerSession) BalanceOfBatch(users []common.Address, provider common.Address) ([]*big.Int, error) {
	return _SandboxServing.Contract.BalanceOfBatch(&_SandboxServing.CallOpts, users, provider)
}

// DomainSeparator is a free data retrieval call binding the contract method 0xf698da25.
//
// Solidity: function domainSeparator() view returns(bytes32)
func (_SandboxServing *SandboxServingCaller) DomainSeparator(opts *bind.CallOpts) ([32]byte, error) {
	var out []interface{}
	err := _SandboxServing.contract.Call(opts, &out, "domainSeparator")

	if err != nil {
		return *new([32]byte), err
	}

	out0 := *abi.ConvertType(out[0], new([32]byte)).(*[32]byte)

	return out0, err

}

// DomainSeparator is a free data retrieval call binding the contract method 0xf698da25.
//
// Solidity: function domainSeparator() view returns(bytes32)
func (_SandboxServing *SandboxServingSession) DomainSeparator() ([32]byte, error) {
	return _SandboxServing.Contract.DomainSeparator(&_SandboxServing.CallOpts)
}

// DomainSeparator is a free data retrieval call binding the contract method 0xf698da25.
//
// Solidity: function domainSeparator() view returns(bytes32)
func (_SandboxServing *SandboxServingCallerSession) DomainSeparator() ([32]byte, error) {
	return _SandboxServing.Contract.DomainSeparator(&_SandboxServing.CallOpts)
}

// GetBalance is a free data retrieval call binding the contract method 0xd4fac45d.
//
// Solidity: function getBalance(address user, address provider) view returns(uint256 balance, uint256 pendingRefund, uint256 refundUnlockAt)
func (_SandboxServing *SandboxServingCaller) GetBalance(opts *bind.CallOpts, user common.Address, provider common.Address) (struct {
	Balance        *big.Int
	PendingRefund  *big.Int
	RefundUnlockAt *big.Int
}, error) {
	var out []interface{}
	err := _SandboxServing.contract.Call(opts, &out, "getBalance", user, provider)

	outstruct := new(struct {
		Balance        *big.Int
		PendingRefund  *big.Int
		RefundUnlockAt *big.Int
	})
	if err != nil {
		return *outstruct, err
	}

	outstruct.Balance = *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)
	outstruct.PendingRefund = *abi.ConvertType(out[1], new(*big.Int)).(**big.Int)
	outstruct.RefundUnlockAt = *abi.ConvertType(out[2], new(*big.Int)).(**big.Int)

	return *outstruct, err

}

// GetBalance is a free data retrieval call binding the contract method 0xd4fac45d.
//
// Solidity: function getBalance(address user, address provider) view returns(uint256 balance, uint256 pendingRefund, uint256 refundUnlockAt)
func (_SandboxServing *SandboxServingSession) GetBalance(user common.Address, provider common.Address) (struct {
	Balance        *big.Int
	PendingRefund  *big.Int
	RefundUnlockAt *big.Int
}, error) {
	return _SandboxServing.Contract.GetBalance(&_SandboxServing.CallOpts, user, provider)
}

// GetBalance is a free data retrieval call binding the contract method 0xd4fac45d.
//
// Solidity: function getBalance(address user, address provider) view returns(uint256 balance, uint256 pendingRefund, uint256 refundUnlockAt)
func (_SandboxServing *SandboxServingCallerSession) GetBalance(user common.Address, provider common.Address) (struct {
	Balance        *big.Int
	PendingRefund  *big.Int
	RefundUnlockAt *big.Int
}, error) {
	return _SandboxServing.Contract.GetBalance(&_SandboxServing.CallOpts, user, provider)
}

// GetLastNonce is a free data retrieval call binding the contract method 0xe3d8bcaf.
//
// Solidity: function getLastNonce(address user, address provider) view returns(uint256)
func (_SandboxServing *SandboxServingCaller) GetLastNonce(opts *bind.CallOpts, user common.Address, provider common.Address) (*big.Int, error) {
	var out []interface{}
	err := _SandboxServing.contract.Call(opts, &out, "getLastNonce", user, provider)

	if err != nil {
		return *new(*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	return out0, err

}

// GetLastNonce is a free data retrieval call binding the contract method 0xe3d8bcaf.
//
// Solidity: function getLastNonce(address user, address provider) view returns(uint256)
func (_SandboxServing *SandboxServingSession) GetLastNonce(user common.Address, provider common.Address) (*big.Int, error) {
	return _SandboxServing.Contract.GetLastNonce(&_SandboxServing.CallOpts, user, provider)
}

// GetLastNonce is a free data retrieval call binding the contract method 0xe3d8bcaf.
//
// Solidity: function getLastNonce(address user, address provider) view returns(uint256)
func (_SandboxServing *SandboxServingCallerSession) GetLastNonce(user common.Address, provider common.Address) (*big.Int, error) {
	return _SandboxServing.Contract.GetLastNonce(&_SandboxServing.CallOpts, user, provider)
}

// GetProviderEarnings is a free data retrieval call binding the contract method 0x1625290f.
//
// Solidity: function getProviderEarnings(address provider) view returns(uint256)
func (_SandboxServing *SandboxServingCaller) GetProviderEarnings(opts *bind.CallOpts, provider common.Address) (*big.Int, error) {
	var out []interface{}
	err := _SandboxServing.contract.Call(opts, &out, "getProviderEarnings", provider)

	if err != nil {
		return *new(*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	return out0, err

}

// GetProviderEarnings is a free data retrieval call binding the contract method 0x1625290f.
//
// Solidity: function getProviderEarnings(address provider) view returns(uint256)
func (_SandboxServing *SandboxServingSession) GetProviderEarnings(provider common.Address) (*big.Int, error) {
	return _SandboxServing.Contract.GetProviderEarnings(&_SandboxServing.CallOpts, provider)
}

// GetProviderEarnings is a free data retrieval call binding the contract method 0x1625290f.
//
// Solidity: function getProviderEarnings(address provider) view returns(uint256)
func (_SandboxServing *SandboxServingCallerSession) GetProviderEarnings(provider common.Address) (*big.Int, error) {
	return _SandboxServing.Contract.GetProviderEarnings(&_SandboxServing.CallOpts, provider)
}

// IsTEEAcknowledged is a free data retrieval call binding the contract method 0xec4e8f2b.
//
// Solidity: function isTEEAcknowledged(address user, address provider) view returns(bool)
func (_SandboxServing *SandboxServingCaller) IsTEEAcknowledged(opts *bind.CallOpts, user common.Address, provider common.Address) (bool, error) {
	var out []interface{}
	err := _SandboxServing.contract.Call(opts, &out, "isTEEAcknowledged", user, provider)

	if err != nil {
		return *new(bool), err
	}

	out0 := *abi.ConvertType(out[0], new(bool)).(*bool)

	return out0, err

}

// IsTEEAcknowledged is a free data retrieval call binding the contract method 0xec4e8f2b.
//
// Solidity: function isTEEAcknowledged(address user, address provider) view returns(bool)
func (_SandboxServing *SandboxServingSession) IsTEEAcknowledged(user common.Address, provider common.Address) (bool, error) {
	return _SandboxServing.Contract.IsTEEAcknowledged(&_SandboxServing.CallOpts, user, provider)
}

// IsTEEAcknowledged is a free data retrieval call binding the contract method 0xec4e8f2b.
//
// Solidity: function isTEEAcknowledged(address user, address provider) view returns(bool)
func (_SandboxServing *SandboxServingCallerSession) IsTEEAcknowledged(user common.Address, provider common.Address) (bool, error) {
	return _SandboxServing.Contract.IsTEEAcknowledged(&_SandboxServing.CallOpts, user, provider)
}

// Owner is a free data retrieval call binding the contract method 0x8da5cb5b.
//
// Solidity: function owner() view returns(address)
func (_SandboxServing *SandboxServingCaller) Owner(opts *bind.CallOpts) (common.Address, error) {
	var out []interface{}
	err := _SandboxServing.contract.Call(opts, &out, "owner")

	if err != nil {
		return *new(common.Address), err
	}

	out0 := *abi.ConvertType(out[0], new(common.Address)).(*common.Address)

	return out0, err

}

// Owner is a free data retrieval call binding the contract method 0x8da5cb5b.
//
// Solidity: function owner() view returns(address)
func (_SandboxServing *SandboxServingSession) Owner() (common.Address, error) {
	return _SandboxServing.Contract.Owner(&_SandboxServing.CallOpts)
}

// Owner is a free data retrieval call binding the contract method 0x8da5cb5b.
//
// Solidity: function owner() view returns(address)
func (_SandboxServing *SandboxServingCallerSession) Owner() (common.Address, error) {
	return _SandboxServing.Contract.Owner(&_SandboxServing.CallOpts)
}

// PreviewSettlementResults is a free data retrieval call binding the contract method 0x28b60476.
//
// Solidity: function previewSettlementResults((address,address,uint256,bytes32,uint256,bytes)[] vouchers) view returns(uint8[] statuses)
func (_SandboxServing *SandboxServingCaller) PreviewSettlementResults(opts *bind.CallOpts, vouchers []SandboxServingSandboxVoucher) ([]uint8, error) {
	var out []interface{}
	err := _SandboxServing.contract.Call(opts, &out, "previewSettlementResults", vouchers)

	if err != nil {
		return *new([]uint8), err
	}

	out0 := *abi.ConvertType(out[0], new([]uint8)).(*[]uint8)

	return out0, err

}

// PreviewSettlementResults is a free data retrieval call binding the contract method 0x28b60476.
//
// Solidity: function previewSettlementResults((address,address,uint256,bytes32,uint256,bytes)[] vouchers) view returns(uint8[] statuses)
func (_SandboxServing *SandboxServingSession) PreviewSettlementResults(vouchers []SandboxServingSandboxVoucher) ([]uint8, error) {
	return _SandboxServing.Contract.PreviewSettlementResults(&_SandboxServing.CallOpts, vouchers)
}

// PreviewSettlementResults is a free data retrieval call binding the contract method 0x28b60476.
//
// Solidity: function previewSettlementResults((address,address,uint256,bytes32,uint256,bytes)[] vouchers) view returns(uint8[] statuses)
func (_SandboxServing *SandboxServingCallerSession) PreviewSettlementResults(vouchers []SandboxServingSandboxVoucher) ([]uint8, error) {
	return _SandboxServing.Contract.PreviewSettlementResults(&_SandboxServing.CallOpts, vouchers)
}

// ProviderEarnings is a free data retrieval call binding the contract method 0x159a6594.
//
// Solidity: function providerEarnings(address ) view returns(uint256)
func (_SandboxServing *SandboxServingCaller) ProviderEarnings(opts *bind.CallOpts, arg0 common.Address) (*big.Int, error) {
	var out []interface{}
	err := _SandboxServing.contract.Call(opts, &out, "providerEarnings", arg0)

	if err != nil {
		return *new(*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	return out0, err

}

// ProviderEarnings is a free data retrieval call binding the contract method 0x159a6594.
//
// Solidity: function providerEarnings(address ) view returns(uint256)
func (_SandboxServing *SandboxServingSession) ProviderEarnings(arg0 common.Address) (*big.Int, error) {
	return _SandboxServing.Contract.ProviderEarnings(&_SandboxServing.CallOpts, arg0)
}

// ProviderEarnings is a free data retrieval call binding the contract method 0x159a6594.
//
// Solidity: function providerEarnings(address ) view returns(uint256)
func (_SandboxServing *SandboxServingCallerSession) ProviderEarnings(arg0 common.Address) (*big.Int, error) {
	return _SandboxServing.Contract.ProviderEarnings(&_SandboxServing.CallOpts, arg0)
}

// ProviderStake is a free data retrieval call binding the contract method 0x324f2dce.
//
// Solidity: function providerStake() view returns(uint256)
func (_SandboxServing *SandboxServingCaller) ProviderStake(opts *bind.CallOpts) (*big.Int, error) {
	var out []interface{}
	err := _SandboxServing.contract.Call(opts, &out, "providerStake")

	if err != nil {
		return *new(*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	return out0, err

}

// ProviderStake is a free data retrieval call binding the contract method 0x324f2dce.
//
// Solidity: function providerStake() view returns(uint256)
func (_SandboxServing *SandboxServingSession) ProviderStake() (*big.Int, error) {
	return _SandboxServing.Contract.ProviderStake(&_SandboxServing.CallOpts)
}

// ProviderStake is a free data retrieval call binding the contract method 0x324f2dce.
//
// Solidity: function providerStake() view returns(uint256)
func (_SandboxServing *SandboxServingCallerSession) ProviderStake() (*big.Int, error) {
	return _SandboxServing.Contract.ProviderStake(&_SandboxServing.CallOpts)
}

// ProviderStakes is a free data retrieval call binding the contract method 0x0d6b4c9f.
//
// Solidity: function providerStakes(address ) view returns(uint256)
func (_SandboxServing *SandboxServingCaller) ProviderStakes(opts *bind.CallOpts, arg0 common.Address) (*big.Int, error) {
	var out []interface{}
	err := _SandboxServing.contract.Call(opts, &out, "providerStakes", arg0)

	if err != nil {
		return *new(*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	return out0, err

}

// ProviderStakes is a free data retrieval call binding the contract method 0x0d6b4c9f.
//
// Solidity: function providerStakes(address ) view returns(uint256)
func (_SandboxServing *SandboxServingSession) ProviderStakes(arg0 common.Address) (*big.Int, error) {
	return _SandboxServing.Contract.ProviderStakes(&_SandboxServing.CallOpts, arg0)
}

// ProviderStakes is a free data retrieval call binding the contract method 0x0d6b4c9f.
//
// Solidity: function providerStakes(address ) view returns(uint256)
func (_SandboxServing *SandboxServingCallerSession) ProviderStakes(arg0 common.Address) (*big.Int, error) {
	return _SandboxServing.Contract.ProviderStakes(&_SandboxServing.CallOpts, arg0)
}

// ServiceExists is a free data retrieval call binding the contract method 0x0a2a8f88.
//
// Solidity: function serviceExists(address ) view returns(bool)
func (_SandboxServing *SandboxServingCaller) ServiceExists(opts *bind.CallOpts, arg0 common.Address) (bool, error) {
	var out []interface{}
	err := _SandboxServing.contract.Call(opts, &out, "serviceExists", arg0)

	if err != nil {
		return *new(bool), err
	}

	out0 := *abi.ConvertType(out[0], new(bool)).(*bool)

	return out0, err

}

// ServiceExists is a free data retrieval call binding the contract method 0x0a2a8f88.
//
// Solidity: function serviceExists(address ) view returns(bool)
func (_SandboxServing *SandboxServingSession) ServiceExists(arg0 common.Address) (bool, error) {
	return _SandboxServing.Contract.ServiceExists(&_SandboxServing.CallOpts, arg0)
}

// ServiceExists is a free data retrieval call binding the contract method 0x0a2a8f88.
//
// Solidity: function serviceExists(address ) view returns(bool)
func (_SandboxServing *SandboxServingCallerSession) ServiceExists(arg0 common.Address) (bool, error) {
	return _SandboxServing.Contract.ServiceExists(&_SandboxServing.CallOpts, arg0)
}

// Services is a free data retrieval call binding the contract method 0x6d966d01.
//
// Solidity: function services(address ) view returns(string url, address teeSignerAddress, uint256 computePricePerMin, uint256 createFee, uint256 signerVersion)
func (_SandboxServing *SandboxServingCaller) Services(opts *bind.CallOpts, arg0 common.Address) (struct {
	Url                string
	TeeSignerAddress   common.Address
	ComputePricePerMin *big.Int
	CreateFee          *big.Int
	SignerVersion      *big.Int
}, error) {
	var out []interface{}
	err := _SandboxServing.contract.Call(opts, &out, "services", arg0)

	outstruct := new(struct {
		Url                string
		TeeSignerAddress   common.Address
		ComputePricePerMin *big.Int
		CreateFee          *big.Int
		SignerVersion      *big.Int
	})
	if err != nil {
		return *outstruct, err
	}

	outstruct.Url = *abi.ConvertType(out[0], new(string)).(*string)
	outstruct.TeeSignerAddress = *abi.ConvertType(out[1], new(common.Address)).(*common.Address)
	outstruct.ComputePricePerMin = *abi.ConvertType(out[2], new(*big.Int)).(**big.Int)
	outstruct.CreateFee = *abi.ConvertType(out[3], new(*big.Int)).(**big.Int)
	outstruct.SignerVersion = *abi.ConvertType(out[4], new(*big.Int)).(**big.Int)

	return *outstruct, err

}

// Services is a free data retrieval call binding the contract method 0x6d966d01.
//
// Solidity: function services(address ) view returns(string url, address teeSignerAddress, uint256 computePricePerMin, uint256 createFee, uint256 signerVersion)
func (_SandboxServing *SandboxServingSession) Services(arg0 common.Address) (struct {
	Url                string
	TeeSignerAddress   common.Address
	ComputePricePerMin *big.Int
	CreateFee          *big.Int
	SignerVersion      *big.Int
}, error) {
	return _SandboxServing.Contract.Services(&_SandboxServing.CallOpts, arg0)
}

// Services is a free data retrieval call binding the contract method 0x6d966d01.
//
// Solidity: function services(address ) view returns(string url, address teeSignerAddress, uint256 computePricePerMin, uint256 createFee, uint256 signerVersion)
func (_SandboxServing *SandboxServingCallerSession) Services(arg0 common.Address) (struct {
	Url                string
	TeeSignerAddress   common.Address
	ComputePricePerMin *big.Int
	CreateFee          *big.Int
	SignerVersion      *big.Int
}, error) {
	return _SandboxServing.Contract.Services(&_SandboxServing.CallOpts, arg0)
}

// AcknowledgeTEESigner is a paid mutator transaction binding the contract method 0x7ff6fc1c.
//
// Solidity: function acknowledgeTEESigner(address provider, bool acknowledged) returns()
func (_SandboxServing *SandboxServingTransactor) AcknowledgeTEESigner(opts *bind.TransactOpts, provider common.Address, acknowledged bool) (*types.Transaction, error) {
	return _SandboxServing.contract.Transact(opts, "acknowledgeTEESigner", provider, acknowledged)
}

// AcknowledgeTEESigner is a paid mutator transaction binding the contract method 0x7ff6fc1c.
//
// Solidity: function acknowledgeTEESigner(address provider, bool acknowledged) returns()
func (_SandboxServing *SandboxServingSession) AcknowledgeTEESigner(provider common.Address, acknowledged bool) (*types.Transaction, error) {
	return _SandboxServing.Contract.AcknowledgeTEESigner(&_SandboxServing.TransactOpts, provider, acknowledged)
}

// AcknowledgeTEESigner is a paid mutator transaction binding the contract method 0x7ff6fc1c.
//
// Solidity: function acknowledgeTEESigner(address provider, bool acknowledged) returns()
func (_SandboxServing *SandboxServingTransactorSession) AcknowledgeTEESigner(provider common.Address, acknowledged bool) (*types.Transaction, error) {
	return _SandboxServing.Contract.AcknowledgeTEESigner(&_SandboxServing.TransactOpts, provider, acknowledged)
}

// AddOrUpdateService is a paid mutator transaction binding the contract method 0xadb6ddeb.
//
// Solidity: function addOrUpdateService(string url, address teeSignerAddress, uint256 computePricePerMin, uint256 createFee) payable returns()
func (_SandboxServing *SandboxServingTransactor) AddOrUpdateService(opts *bind.TransactOpts, url string, teeSignerAddress common.Address, computePricePerMin *big.Int, createFee *big.Int) (*types.Transaction, error) {
	return _SandboxServing.contract.Transact(opts, "addOrUpdateService", url, teeSignerAddress, computePricePerMin, createFee)
}

// AddOrUpdateService is a paid mutator transaction binding the contract method 0xadb6ddeb.
//
// Solidity: function addOrUpdateService(string url, address teeSignerAddress, uint256 computePricePerMin, uint256 createFee) payable returns()
func (_SandboxServing *SandboxServingSession) AddOrUpdateService(url string, teeSignerAddress common.Address, computePricePerMin *big.Int, createFee *big.Int) (*types.Transaction, error) {
	return _SandboxServing.Contract.AddOrUpdateService(&_SandboxServing.TransactOpts, url, teeSignerAddress, computePricePerMin, createFee)
}

// AddOrUpdateService is a paid mutator transaction binding the contract method 0xadb6ddeb.
//
// Solidity: function addOrUpdateService(string url, address teeSignerAddress, uint256 computePricePerMin, uint256 createFee) payable returns()
func (_SandboxServing *SandboxServingTransactorSession) AddOrUpdateService(url string, teeSignerAddress common.Address, computePricePerMin *big.Int, createFee *big.Int) (*types.Transaction, error) {
	return _SandboxServing.Contract.AddOrUpdateService(&_SandboxServing.TransactOpts, url, teeSignerAddress, computePricePerMin, createFee)
}

// Deposit is a paid mutator transaction binding the contract method 0xf9609f08.
//
// Solidity: function deposit(address recipient, address provider) payable returns()
func (_SandboxServing *SandboxServingTransactor) Deposit(opts *bind.TransactOpts, recipient common.Address, provider common.Address) (*types.Transaction, error) {
	return _SandboxServing.contract.Transact(opts, "deposit", recipient, provider)
}

// Deposit is a paid mutator transaction binding the contract method 0xf9609f08.
//
// Solidity: function deposit(address recipient, address provider) payable returns()
func (_SandboxServing *SandboxServingSession) Deposit(recipient common.Address, provider common.Address) (*types.Transaction, error) {
	return _SandboxServing.Contract.Deposit(&_SandboxServing.TransactOpts, recipient, provider)
}

// Deposit is a paid mutator transaction binding the contract method 0xf9609f08.
//
// Solidity: function deposit(address recipient, address provider) payable returns()
func (_SandboxServing *SandboxServingTransactorSession) Deposit(recipient common.Address, provider common.Address) (*types.Transaction, error) {
	return _SandboxServing.Contract.Deposit(&_SandboxServing.TransactOpts, recipient, provider)
}

// Initialize is a paid mutator transaction binding the contract method 0xfe4b84df.
//
// Solidity: function initialize(uint256 providerStake_) returns()
func (_SandboxServing *SandboxServingTransactor) Initialize(opts *bind.TransactOpts, providerStake_ *big.Int) (*types.Transaction, error) {
	return _SandboxServing.contract.Transact(opts, "initialize", providerStake_)
}

// Initialize is a paid mutator transaction binding the contract method 0xfe4b84df.
//
// Solidity: function initialize(uint256 providerStake_) returns()
func (_SandboxServing *SandboxServingSession) Initialize(providerStake_ *big.Int) (*types.Transaction, error) {
	return _SandboxServing.Contract.Initialize(&_SandboxServing.TransactOpts, providerStake_)
}

// Initialize is a paid mutator transaction binding the contract method 0xfe4b84df.
//
// Solidity: function initialize(uint256 providerStake_) returns()
func (_SandboxServing *SandboxServingTransactorSession) Initialize(providerStake_ *big.Int) (*types.Transaction, error) {
	return _SandboxServing.Contract.Initialize(&_SandboxServing.TransactOpts, providerStake_)
}

// RequestRefund is a paid mutator transaction binding the contract method 0x99652de7.
//
// Solidity: function requestRefund(address provider, uint256 amount) returns()
func (_SandboxServing *SandboxServingTransactor) RequestRefund(opts *bind.TransactOpts, provider common.Address, amount *big.Int) (*types.Transaction, error) {
	return _SandboxServing.contract.Transact(opts, "requestRefund", provider, amount)
}

// RequestRefund is a paid mutator transaction binding the contract method 0x99652de7.
//
// Solidity: function requestRefund(address provider, uint256 amount) returns()
func (_SandboxServing *SandboxServingSession) RequestRefund(provider common.Address, amount *big.Int) (*types.Transaction, error) {
	return _SandboxServing.Contract.RequestRefund(&_SandboxServing.TransactOpts, provider, amount)
}

// RequestRefund is a paid mutator transaction binding the contract method 0x99652de7.
//
// Solidity: function requestRefund(address provider, uint256 amount) returns()
func (_SandboxServing *SandboxServingTransactorSession) RequestRefund(provider common.Address, amount *big.Int) (*types.Transaction, error) {
	return _SandboxServing.Contract.RequestRefund(&_SandboxServing.TransactOpts, provider, amount)
}

// SetProviderStake is a paid mutator transaction binding the contract method 0x5d5c2422.
//
// Solidity: function setProviderStake(uint256 newStake) returns()
func (_SandboxServing *SandboxServingTransactor) SetProviderStake(opts *bind.TransactOpts, newStake *big.Int) (*types.Transaction, error) {
	return _SandboxServing.contract.Transact(opts, "setProviderStake", newStake)
}

// SetProviderStake is a paid mutator transaction binding the contract method 0x5d5c2422.
//
// Solidity: function setProviderStake(uint256 newStake) returns()
func (_SandboxServing *SandboxServingSession) SetProviderStake(newStake *big.Int) (*types.Transaction, error) {
	return _SandboxServing.Contract.SetProviderStake(&_SandboxServing.TransactOpts, newStake)
}

// SetProviderStake is a paid mutator transaction binding the contract method 0x5d5c2422.
//
// Solidity: function setProviderStake(uint256 newStake) returns()
func (_SandboxServing *SandboxServingTransactorSession) SetProviderStake(newStake *big.Int) (*types.Transaction, error) {
	return _SandboxServing.Contract.SetProviderStake(&_SandboxServing.TransactOpts, newStake)
}

// SettleFeesWithTEE is a paid mutator transaction binding the contract method 0x8be74119.
//
// Solidity: function settleFeesWithTEE((address,address,uint256,bytes32,uint256,bytes)[] vouchers) returns(uint8[] statuses)
func (_SandboxServing *SandboxServingTransactor) SettleFeesWithTEE(opts *bind.TransactOpts, vouchers []SandboxServingSandboxVoucher) (*types.Transaction, error) {
	return _SandboxServing.contract.Transact(opts, "settleFeesWithTEE", vouchers)
}

// SettleFeesWithTEE is a paid mutator transaction binding the contract method 0x8be74119.
//
// Solidity: function settleFeesWithTEE((address,address,uint256,bytes32,uint256,bytes)[] vouchers) returns(uint8[] statuses)
func (_SandboxServing *SandboxServingSession) SettleFeesWithTEE(vouchers []SandboxServingSandboxVoucher) (*types.Transaction, error) {
	return _SandboxServing.Contract.SettleFeesWithTEE(&_SandboxServing.TransactOpts, vouchers)
}

// SettleFeesWithTEE is a paid mutator transaction binding the contract method 0x8be74119.
//
// Solidity: function settleFeesWithTEE((address,address,uint256,bytes32,uint256,bytes)[] vouchers) returns(uint8[] statuses)
func (_SandboxServing *SandboxServingTransactorSession) SettleFeesWithTEE(vouchers []SandboxServingSandboxVoucher) (*types.Transaction, error) {
	return _SandboxServing.Contract.SettleFeesWithTEE(&_SandboxServing.TransactOpts, vouchers)
}

// TransferOwnership is a paid mutator transaction binding the contract method 0xf2fde38b.
//
// Solidity: function transferOwnership(address newOwner) returns()
func (_SandboxServing *SandboxServingTransactor) TransferOwnership(opts *bind.TransactOpts, newOwner common.Address) (*types.Transaction, error) {
	return _SandboxServing.contract.Transact(opts, "transferOwnership", newOwner)
}

// TransferOwnership is a paid mutator transaction binding the contract method 0xf2fde38b.
//
// Solidity: function transferOwnership(address newOwner) returns()
func (_SandboxServing *SandboxServingSession) TransferOwnership(newOwner common.Address) (*types.Transaction, error) {
	return _SandboxServing.Contract.TransferOwnership(&_SandboxServing.TransactOpts, newOwner)
}

// TransferOwnership is a paid mutator transaction binding the contract method 0xf2fde38b.
//
// Solidity: function transferOwnership(address newOwner) returns()
func (_SandboxServing *SandboxServingTransactorSession) TransferOwnership(newOwner common.Address) (*types.Transaction, error) {
	return _SandboxServing.Contract.TransferOwnership(&_SandboxServing.TransactOpts, newOwner)
}

// WithdrawEarnings is a paid mutator transaction binding the contract method 0xb73c6ce9.
//
// Solidity: function withdrawEarnings() returns()
func (_SandboxServing *SandboxServingTransactor) WithdrawEarnings(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _SandboxServing.contract.Transact(opts, "withdrawEarnings")
}

// WithdrawEarnings is a paid mutator transaction binding the contract method 0xb73c6ce9.
//
// Solidity: function withdrawEarnings() returns()
func (_SandboxServing *SandboxServingSession) WithdrawEarnings() (*types.Transaction, error) {
	return _SandboxServing.Contract.WithdrawEarnings(&_SandboxServing.TransactOpts)
}

// WithdrawEarnings is a paid mutator transaction binding the contract method 0xb73c6ce9.
//
// Solidity: function withdrawEarnings() returns()
func (_SandboxServing *SandboxServingTransactorSession) WithdrawEarnings() (*types.Transaction, error) {
	return _SandboxServing.Contract.WithdrawEarnings(&_SandboxServing.TransactOpts)
}

// WithdrawRefund is a paid mutator transaction binding the contract method 0xa16c86f7.
//
// Solidity: function withdrawRefund(address provider) returns()
func (_SandboxServing *SandboxServingTransactor) WithdrawRefund(opts *bind.TransactOpts, provider common.Address) (*types.Transaction, error) {
	return _SandboxServing.contract.Transact(opts, "withdrawRefund", provider)
}

// WithdrawRefund is a paid mutator transaction binding the contract method 0xa16c86f7.
//
// Solidity: function withdrawRefund(address provider) returns()
func (_SandboxServing *SandboxServingSession) WithdrawRefund(provider common.Address) (*types.Transaction, error) {
	return _SandboxServing.Contract.WithdrawRefund(&_SandboxServing.TransactOpts, provider)
}

// WithdrawRefund is a paid mutator transaction binding the contract method 0xa16c86f7.
//
// Solidity: function withdrawRefund(address provider) returns()
func (_SandboxServing *SandboxServingTransactorSession) WithdrawRefund(provider common.Address) (*types.Transaction, error) {
	return _SandboxServing.Contract.WithdrawRefund(&_SandboxServing.TransactOpts, provider)
}

// SandboxServingDepositedIterator is returned from FilterDeposited and is used to iterate over the raw logs and unpacked data for Deposited events raised by the SandboxServing contract.
type SandboxServingDepositedIterator struct {
	Event *SandboxServingDeposited // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  ethereum.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *SandboxServingDepositedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(SandboxServingDeposited)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(SandboxServingDeposited)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *SandboxServingDepositedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *SandboxServingDepositedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// SandboxServingDeposited represents a Deposited event raised by the SandboxServing contract.
type SandboxServingDeposited struct {
	Recipient common.Address
	Provider  common.Address
	Sender    common.Address
	Amount    *big.Int
	Raw       types.Log // Blockchain specific contextual infos
}

// FilterDeposited is a free log retrieval operation binding the contract event 0x4174a9435a04d04d274c76779cad136a41fde6937c56241c09ab9d3c7064a1a9.
//
// Solidity: event Deposited(address indexed recipient, address indexed provider, address indexed sender, uint256 amount)
func (_SandboxServing *SandboxServingFilterer) FilterDeposited(opts *bind.FilterOpts, recipient []common.Address, provider []common.Address, sender []common.Address) (*SandboxServingDepositedIterator, error) {

	var recipientRule []interface{}
	for _, recipientItem := range recipient {
		recipientRule = append(recipientRule, recipientItem)
	}
	var providerRule []interface{}
	for _, providerItem := range provider {
		providerRule = append(providerRule, providerItem)
	}
	var senderRule []interface{}
	for _, senderItem := range sender {
		senderRule = append(senderRule, senderItem)
	}

	logs, sub, err := _SandboxServing.contract.FilterLogs(opts, "Deposited", recipientRule, providerRule, senderRule)
	if err != nil {
		return nil, err
	}
	return &SandboxServingDepositedIterator{contract: _SandboxServing.contract, event: "Deposited", logs: logs, sub: sub}, nil
}

// WatchDeposited is a free log subscription operation binding the contract event 0x4174a9435a04d04d274c76779cad136a41fde6937c56241c09ab9d3c7064a1a9.
//
// Solidity: event Deposited(address indexed recipient, address indexed provider, address indexed sender, uint256 amount)
func (_SandboxServing *SandboxServingFilterer) WatchDeposited(opts *bind.WatchOpts, sink chan<- *SandboxServingDeposited, recipient []common.Address, provider []common.Address, sender []common.Address) (event.Subscription, error) {

	var recipientRule []interface{}
	for _, recipientItem := range recipient {
		recipientRule = append(recipientRule, recipientItem)
	}
	var providerRule []interface{}
	for _, providerItem := range provider {
		providerRule = append(providerRule, providerItem)
	}
	var senderRule []interface{}
	for _, senderItem := range sender {
		senderRule = append(senderRule, senderItem)
	}

	logs, sub, err := _SandboxServing.contract.WatchLogs(opts, "Deposited", recipientRule, providerRule, senderRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(SandboxServingDeposited)
				if err := _SandboxServing.contract.UnpackLog(event, "Deposited", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseDeposited is a log parse operation binding the contract event 0x4174a9435a04d04d274c76779cad136a41fde6937c56241c09ab9d3c7064a1a9.
//
// Solidity: event Deposited(address indexed recipient, address indexed provider, address indexed sender, uint256 amount)
func (_SandboxServing *SandboxServingFilterer) ParseDeposited(log types.Log) (*SandboxServingDeposited, error) {
	event := new(SandboxServingDeposited)
	if err := _SandboxServing.contract.UnpackLog(event, "Deposited", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// SandboxServingEarningsWithdrawnIterator is returned from FilterEarningsWithdrawn and is used to iterate over the raw logs and unpacked data for EarningsWithdrawn events raised by the SandboxServing contract.
type SandboxServingEarningsWithdrawnIterator struct {
	Event *SandboxServingEarningsWithdrawn // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  ethereum.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *SandboxServingEarningsWithdrawnIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(SandboxServingEarningsWithdrawn)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(SandboxServingEarningsWithdrawn)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *SandboxServingEarningsWithdrawnIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *SandboxServingEarningsWithdrawnIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// SandboxServingEarningsWithdrawn represents a EarningsWithdrawn event raised by the SandboxServing contract.
type SandboxServingEarningsWithdrawn struct {
	Provider common.Address
	Amount   *big.Int
	Raw      types.Log // Blockchain specific contextual infos
}

// FilterEarningsWithdrawn is a free log retrieval operation binding the contract event 0x48dc35af7b45e2a81fffad55f6e2fafacdb1d3d0d50d24ebdc16324f5ba757f1.
//
// Solidity: event EarningsWithdrawn(address indexed provider, uint256 amount)
func (_SandboxServing *SandboxServingFilterer) FilterEarningsWithdrawn(opts *bind.FilterOpts, provider []common.Address) (*SandboxServingEarningsWithdrawnIterator, error) {

	var providerRule []interface{}
	for _, providerItem := range provider {
		providerRule = append(providerRule, providerItem)
	}

	logs, sub, err := _SandboxServing.contract.FilterLogs(opts, "EarningsWithdrawn", providerRule)
	if err != nil {
		return nil, err
	}
	return &SandboxServingEarningsWithdrawnIterator{contract: _SandboxServing.contract, event: "EarningsWithdrawn", logs: logs, sub: sub}, nil
}

// WatchEarningsWithdrawn is a free log subscription operation binding the contract event 0x48dc35af7b45e2a81fffad55f6e2fafacdb1d3d0d50d24ebdc16324f5ba757f1.
//
// Solidity: event EarningsWithdrawn(address indexed provider, uint256 amount)
func (_SandboxServing *SandboxServingFilterer) WatchEarningsWithdrawn(opts *bind.WatchOpts, sink chan<- *SandboxServingEarningsWithdrawn, provider []common.Address) (event.Subscription, error) {

	var providerRule []interface{}
	for _, providerItem := range provider {
		providerRule = append(providerRule, providerItem)
	}

	logs, sub, err := _SandboxServing.contract.WatchLogs(opts, "EarningsWithdrawn", providerRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(SandboxServingEarningsWithdrawn)
				if err := _SandboxServing.contract.UnpackLog(event, "EarningsWithdrawn", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseEarningsWithdrawn is a log parse operation binding the contract event 0x48dc35af7b45e2a81fffad55f6e2fafacdb1d3d0d50d24ebdc16324f5ba757f1.
//
// Solidity: event EarningsWithdrawn(address indexed provider, uint256 amount)
func (_SandboxServing *SandboxServingFilterer) ParseEarningsWithdrawn(log types.Log) (*SandboxServingEarningsWithdrawn, error) {
	event := new(SandboxServingEarningsWithdrawn)
	if err := _SandboxServing.contract.UnpackLog(event, "EarningsWithdrawn", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// SandboxServingOwnershipTransferredIterator is returned from FilterOwnershipTransferred and is used to iterate over the raw logs and unpacked data for OwnershipTransferred events raised by the SandboxServing contract.
type SandboxServingOwnershipTransferredIterator struct {
	Event *SandboxServingOwnershipTransferred // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  ethereum.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *SandboxServingOwnershipTransferredIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(SandboxServingOwnershipTransferred)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(SandboxServingOwnershipTransferred)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *SandboxServingOwnershipTransferredIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *SandboxServingOwnershipTransferredIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// SandboxServingOwnershipTransferred represents a OwnershipTransferred event raised by the SandboxServing contract.
type SandboxServingOwnershipTransferred struct {
	PreviousOwner common.Address
	NewOwner      common.Address
	Raw           types.Log // Blockchain specific contextual infos
}

// FilterOwnershipTransferred is a free log retrieval operation binding the contract event 0x8be0079c531659141344cd1fd0a4f28419497f9722a3daafe3b4186f6b6457e0.
//
// Solidity: event OwnershipTransferred(address indexed previousOwner, address indexed newOwner)
func (_SandboxServing *SandboxServingFilterer) FilterOwnershipTransferred(opts *bind.FilterOpts, previousOwner []common.Address, newOwner []common.Address) (*SandboxServingOwnershipTransferredIterator, error) {

	var previousOwnerRule []interface{}
	for _, previousOwnerItem := range previousOwner {
		previousOwnerRule = append(previousOwnerRule, previousOwnerItem)
	}
	var newOwnerRule []interface{}
	for _, newOwnerItem := range newOwner {
		newOwnerRule = append(newOwnerRule, newOwnerItem)
	}

	logs, sub, err := _SandboxServing.contract.FilterLogs(opts, "OwnershipTransferred", previousOwnerRule, newOwnerRule)
	if err != nil {
		return nil, err
	}
	return &SandboxServingOwnershipTransferredIterator{contract: _SandboxServing.contract, event: "OwnershipTransferred", logs: logs, sub: sub}, nil
}

// WatchOwnershipTransferred is a free log subscription operation binding the contract event 0x8be0079c531659141344cd1fd0a4f28419497f9722a3daafe3b4186f6b6457e0.
//
// Solidity: event OwnershipTransferred(address indexed previousOwner, address indexed newOwner)
func (_SandboxServing *SandboxServingFilterer) WatchOwnershipTransferred(opts *bind.WatchOpts, sink chan<- *SandboxServingOwnershipTransferred, previousOwner []common.Address, newOwner []common.Address) (event.Subscription, error) {

	var previousOwnerRule []interface{}
	for _, previousOwnerItem := range previousOwner {
		previousOwnerRule = append(previousOwnerRule, previousOwnerItem)
	}
	var newOwnerRule []interface{}
	for _, newOwnerItem := range newOwner {
		newOwnerRule = append(newOwnerRule, newOwnerItem)
	}

	logs, sub, err := _SandboxServing.contract.WatchLogs(opts, "OwnershipTransferred", previousOwnerRule, newOwnerRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(SandboxServingOwnershipTransferred)
				if err := _SandboxServing.contract.UnpackLog(event, "OwnershipTransferred", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseOwnershipTransferred is a log parse operation binding the contract event 0x8be0079c531659141344cd1fd0a4f28419497f9722a3daafe3b4186f6b6457e0.
//
// Solidity: event OwnershipTransferred(address indexed previousOwner, address indexed newOwner)
func (_SandboxServing *SandboxServingFilterer) ParseOwnershipTransferred(log types.Log) (*SandboxServingOwnershipTransferred, error) {
	event := new(SandboxServingOwnershipTransferred)
	if err := _SandboxServing.contract.UnpackLog(event, "OwnershipTransferred", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// SandboxServingProviderStakeUpdatedIterator is returned from FilterProviderStakeUpdated and is used to iterate over the raw logs and unpacked data for ProviderStakeUpdated events raised by the SandboxServing contract.
type SandboxServingProviderStakeUpdatedIterator struct {
	Event *SandboxServingProviderStakeUpdated // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  ethereum.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *SandboxServingProviderStakeUpdatedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(SandboxServingProviderStakeUpdated)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(SandboxServingProviderStakeUpdated)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *SandboxServingProviderStakeUpdatedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *SandboxServingProviderStakeUpdatedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// SandboxServingProviderStakeUpdated represents a ProviderStakeUpdated event raised by the SandboxServing contract.
type SandboxServingProviderStakeUpdated struct {
	OldStake *big.Int
	NewStake *big.Int
	Raw      types.Log // Blockchain specific contextual infos
}

// FilterProviderStakeUpdated is a free log retrieval operation binding the contract event 0xaf9afb0be8799af0bf3b72da97f9e29db192a7fdd6544074ae08a889796bfc1b.
//
// Solidity: event ProviderStakeUpdated(uint256 oldStake, uint256 newStake)
func (_SandboxServing *SandboxServingFilterer) FilterProviderStakeUpdated(opts *bind.FilterOpts) (*SandboxServingProviderStakeUpdatedIterator, error) {

	logs, sub, err := _SandboxServing.contract.FilterLogs(opts, "ProviderStakeUpdated")
	if err != nil {
		return nil, err
	}
	return &SandboxServingProviderStakeUpdatedIterator{contract: _SandboxServing.contract, event: "ProviderStakeUpdated", logs: logs, sub: sub}, nil
}

// WatchProviderStakeUpdated is a free log subscription operation binding the contract event 0xaf9afb0be8799af0bf3b72da97f9e29db192a7fdd6544074ae08a889796bfc1b.
//
// Solidity: event ProviderStakeUpdated(uint256 oldStake, uint256 newStake)
func (_SandboxServing *SandboxServingFilterer) WatchProviderStakeUpdated(opts *bind.WatchOpts, sink chan<- *SandboxServingProviderStakeUpdated) (event.Subscription, error) {

	logs, sub, err := _SandboxServing.contract.WatchLogs(opts, "ProviderStakeUpdated")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(SandboxServingProviderStakeUpdated)
				if err := _SandboxServing.contract.UnpackLog(event, "ProviderStakeUpdated", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseProviderStakeUpdated is a log parse operation binding the contract event 0xaf9afb0be8799af0bf3b72da97f9e29db192a7fdd6544074ae08a889796bfc1b.
//
// Solidity: event ProviderStakeUpdated(uint256 oldStake, uint256 newStake)
func (_SandboxServing *SandboxServingFilterer) ParseProviderStakeUpdated(log types.Log) (*SandboxServingProviderStakeUpdated, error) {
	event := new(SandboxServingProviderStakeUpdated)
	if err := _SandboxServing.contract.UnpackLog(event, "ProviderStakeUpdated", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// SandboxServingRefundRequestedIterator is returned from FilterRefundRequested and is used to iterate over the raw logs and unpacked data for RefundRequested events raised by the SandboxServing contract.
type SandboxServingRefundRequestedIterator struct {
	Event *SandboxServingRefundRequested // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  ethereum.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *SandboxServingRefundRequestedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(SandboxServingRefundRequested)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(SandboxServingRefundRequested)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *SandboxServingRefundRequestedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *SandboxServingRefundRequestedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// SandboxServingRefundRequested represents a RefundRequested event raised by the SandboxServing contract.
type SandboxServingRefundRequested struct {
	User     common.Address
	Provider common.Address
	Amount   *big.Int
	UnlockAt *big.Int
	Raw      types.Log // Blockchain specific contextual infos
}

// FilterRefundRequested is a free log retrieval operation binding the contract event 0x54377dfdebf06f6df53fbda737d2dcd7e141f95bbfb0c1223437e856b9de3ac3.
//
// Solidity: event RefundRequested(address indexed user, address indexed provider, uint256 amount, uint256 unlockAt)
func (_SandboxServing *SandboxServingFilterer) FilterRefundRequested(opts *bind.FilterOpts, user []common.Address, provider []common.Address) (*SandboxServingRefundRequestedIterator, error) {

	var userRule []interface{}
	for _, userItem := range user {
		userRule = append(userRule, userItem)
	}
	var providerRule []interface{}
	for _, providerItem := range provider {
		providerRule = append(providerRule, providerItem)
	}

	logs, sub, err := _SandboxServing.contract.FilterLogs(opts, "RefundRequested", userRule, providerRule)
	if err != nil {
		return nil, err
	}
	return &SandboxServingRefundRequestedIterator{contract: _SandboxServing.contract, event: "RefundRequested", logs: logs, sub: sub}, nil
}

// WatchRefundRequested is a free log subscription operation binding the contract event 0x54377dfdebf06f6df53fbda737d2dcd7e141f95bbfb0c1223437e856b9de3ac3.
//
// Solidity: event RefundRequested(address indexed user, address indexed provider, uint256 amount, uint256 unlockAt)
func (_SandboxServing *SandboxServingFilterer) WatchRefundRequested(opts *bind.WatchOpts, sink chan<- *SandboxServingRefundRequested, user []common.Address, provider []common.Address) (event.Subscription, error) {

	var userRule []interface{}
	for _, userItem := range user {
		userRule = append(userRule, userItem)
	}
	var providerRule []interface{}
	for _, providerItem := range provider {
		providerRule = append(providerRule, providerItem)
	}

	logs, sub, err := _SandboxServing.contract.WatchLogs(opts, "RefundRequested", userRule, providerRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(SandboxServingRefundRequested)
				if err := _SandboxServing.contract.UnpackLog(event, "RefundRequested", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseRefundRequested is a log parse operation binding the contract event 0x54377dfdebf06f6df53fbda737d2dcd7e141f95bbfb0c1223437e856b9de3ac3.
//
// Solidity: event RefundRequested(address indexed user, address indexed provider, uint256 amount, uint256 unlockAt)
func (_SandboxServing *SandboxServingFilterer) ParseRefundRequested(log types.Log) (*SandboxServingRefundRequested, error) {
	event := new(SandboxServingRefundRequested)
	if err := _SandboxServing.contract.UnpackLog(event, "RefundRequested", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// SandboxServingRefundWithdrawnIterator is returned from FilterRefundWithdrawn and is used to iterate over the raw logs and unpacked data for RefundWithdrawn events raised by the SandboxServing contract.
type SandboxServingRefundWithdrawnIterator struct {
	Event *SandboxServingRefundWithdrawn // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  ethereum.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *SandboxServingRefundWithdrawnIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(SandboxServingRefundWithdrawn)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(SandboxServingRefundWithdrawn)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *SandboxServingRefundWithdrawnIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *SandboxServingRefundWithdrawnIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// SandboxServingRefundWithdrawn represents a RefundWithdrawn event raised by the SandboxServing contract.
type SandboxServingRefundWithdrawn struct {
	User     common.Address
	Provider common.Address
	Amount   *big.Int
	Raw      types.Log // Blockchain specific contextual infos
}

// FilterRefundWithdrawn is a free log retrieval operation binding the contract event 0xd55b5fe81317b854ac11454adf7e5a9a0adf69184d643ef9ae6bfda6a015c5bc.
//
// Solidity: event RefundWithdrawn(address indexed user, address indexed provider, uint256 amount)
func (_SandboxServing *SandboxServingFilterer) FilterRefundWithdrawn(opts *bind.FilterOpts, user []common.Address, provider []common.Address) (*SandboxServingRefundWithdrawnIterator, error) {

	var userRule []interface{}
	for _, userItem := range user {
		userRule = append(userRule, userItem)
	}
	var providerRule []interface{}
	for _, providerItem := range provider {
		providerRule = append(providerRule, providerItem)
	}

	logs, sub, err := _SandboxServing.contract.FilterLogs(opts, "RefundWithdrawn", userRule, providerRule)
	if err != nil {
		return nil, err
	}
	return &SandboxServingRefundWithdrawnIterator{contract: _SandboxServing.contract, event: "RefundWithdrawn", logs: logs, sub: sub}, nil
}

// WatchRefundWithdrawn is a free log subscription operation binding the contract event 0xd55b5fe81317b854ac11454adf7e5a9a0adf69184d643ef9ae6bfda6a015c5bc.
//
// Solidity: event RefundWithdrawn(address indexed user, address indexed provider, uint256 amount)
func (_SandboxServing *SandboxServingFilterer) WatchRefundWithdrawn(opts *bind.WatchOpts, sink chan<- *SandboxServingRefundWithdrawn, user []common.Address, provider []common.Address) (event.Subscription, error) {

	var userRule []interface{}
	for _, userItem := range user {
		userRule = append(userRule, userItem)
	}
	var providerRule []interface{}
	for _, providerItem := range provider {
		providerRule = append(providerRule, providerItem)
	}

	logs, sub, err := _SandboxServing.contract.WatchLogs(opts, "RefundWithdrawn", userRule, providerRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(SandboxServingRefundWithdrawn)
				if err := _SandboxServing.contract.UnpackLog(event, "RefundWithdrawn", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseRefundWithdrawn is a log parse operation binding the contract event 0xd55b5fe81317b854ac11454adf7e5a9a0adf69184d643ef9ae6bfda6a015c5bc.
//
// Solidity: event RefundWithdrawn(address indexed user, address indexed provider, uint256 amount)
func (_SandboxServing *SandboxServingFilterer) ParseRefundWithdrawn(log types.Log) (*SandboxServingRefundWithdrawn, error) {
	event := new(SandboxServingRefundWithdrawn)
	if err := _SandboxServing.contract.UnpackLog(event, "RefundWithdrawn", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// SandboxServingServiceUpdatedIterator is returned from FilterServiceUpdated and is used to iterate over the raw logs and unpacked data for ServiceUpdated events raised by the SandboxServing contract.
type SandboxServingServiceUpdatedIterator struct {
	Event *SandboxServingServiceUpdated // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  ethereum.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *SandboxServingServiceUpdatedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(SandboxServingServiceUpdated)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(SandboxServingServiceUpdated)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *SandboxServingServiceUpdatedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *SandboxServingServiceUpdatedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// SandboxServingServiceUpdated represents a ServiceUpdated event raised by the SandboxServing contract.
type SandboxServingServiceUpdated struct {
	Provider         common.Address
	Url              string
	TeeSignerAddress common.Address
	SignerVersion    *big.Int
	Raw              types.Log // Blockchain specific contextual infos
}

// FilterServiceUpdated is a free log retrieval operation binding the contract event 0xe8f0f62d906ac494985f0f34a2c1b08eb0d700b88ac0787b1eed29a3ae2dafe6.
//
// Solidity: event ServiceUpdated(address indexed provider, string url, address teeSignerAddress, uint256 signerVersion)
func (_SandboxServing *SandboxServingFilterer) FilterServiceUpdated(opts *bind.FilterOpts, provider []common.Address) (*SandboxServingServiceUpdatedIterator, error) {

	var providerRule []interface{}
	for _, providerItem := range provider {
		providerRule = append(providerRule, providerItem)
	}

	logs, sub, err := _SandboxServing.contract.FilterLogs(opts, "ServiceUpdated", providerRule)
	if err != nil {
		return nil, err
	}
	return &SandboxServingServiceUpdatedIterator{contract: _SandboxServing.contract, event: "ServiceUpdated", logs: logs, sub: sub}, nil
}

// WatchServiceUpdated is a free log subscription operation binding the contract event 0xe8f0f62d906ac494985f0f34a2c1b08eb0d700b88ac0787b1eed29a3ae2dafe6.
//
// Solidity: event ServiceUpdated(address indexed provider, string url, address teeSignerAddress, uint256 signerVersion)
func (_SandboxServing *SandboxServingFilterer) WatchServiceUpdated(opts *bind.WatchOpts, sink chan<- *SandboxServingServiceUpdated, provider []common.Address) (event.Subscription, error) {

	var providerRule []interface{}
	for _, providerItem := range provider {
		providerRule = append(providerRule, providerItem)
	}

	logs, sub, err := _SandboxServing.contract.WatchLogs(opts, "ServiceUpdated", providerRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(SandboxServingServiceUpdated)
				if err := _SandboxServing.contract.UnpackLog(event, "ServiceUpdated", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseServiceUpdated is a log parse operation binding the contract event 0xe8f0f62d906ac494985f0f34a2c1b08eb0d700b88ac0787b1eed29a3ae2dafe6.
//
// Solidity: event ServiceUpdated(address indexed provider, string url, address teeSignerAddress, uint256 signerVersion)
func (_SandboxServing *SandboxServingFilterer) ParseServiceUpdated(log types.Log) (*SandboxServingServiceUpdated, error) {
	event := new(SandboxServingServiceUpdated)
	if err := _SandboxServing.contract.UnpackLog(event, "ServiceUpdated", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// SandboxServingTEESignerAcknowledgedIterator is returned from FilterTEESignerAcknowledged and is used to iterate over the raw logs and unpacked data for TEESignerAcknowledged events raised by the SandboxServing contract.
type SandboxServingTEESignerAcknowledgedIterator struct {
	Event *SandboxServingTEESignerAcknowledged // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  ethereum.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *SandboxServingTEESignerAcknowledgedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(SandboxServingTEESignerAcknowledged)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(SandboxServingTEESignerAcknowledged)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *SandboxServingTEESignerAcknowledgedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *SandboxServingTEESignerAcknowledgedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// SandboxServingTEESignerAcknowledged represents a TEESignerAcknowledged event raised by the SandboxServing contract.
type SandboxServingTEESignerAcknowledged struct {
	User         common.Address
	Provider     common.Address
	Acknowledged bool
	Raw          types.Log // Blockchain specific contextual infos
}

// FilterTEESignerAcknowledged is a free log retrieval operation binding the contract event 0x0002df5a9025c3e501b00d10c3bbfc3d8d37dbf5c904d758c9267a5a3880ee6f.
//
// Solidity: event TEESignerAcknowledged(address indexed user, address indexed provider, bool acknowledged)
func (_SandboxServing *SandboxServingFilterer) FilterTEESignerAcknowledged(opts *bind.FilterOpts, user []common.Address, provider []common.Address) (*SandboxServingTEESignerAcknowledgedIterator, error) {

	var userRule []interface{}
	for _, userItem := range user {
		userRule = append(userRule, userItem)
	}
	var providerRule []interface{}
	for _, providerItem := range provider {
		providerRule = append(providerRule, providerItem)
	}

	logs, sub, err := _SandboxServing.contract.FilterLogs(opts, "TEESignerAcknowledged", userRule, providerRule)
	if err != nil {
		return nil, err
	}
	return &SandboxServingTEESignerAcknowledgedIterator{contract: _SandboxServing.contract, event: "TEESignerAcknowledged", logs: logs, sub: sub}, nil
}

// WatchTEESignerAcknowledged is a free log subscription operation binding the contract event 0x0002df5a9025c3e501b00d10c3bbfc3d8d37dbf5c904d758c9267a5a3880ee6f.
//
// Solidity: event TEESignerAcknowledged(address indexed user, address indexed provider, bool acknowledged)
func (_SandboxServing *SandboxServingFilterer) WatchTEESignerAcknowledged(opts *bind.WatchOpts, sink chan<- *SandboxServingTEESignerAcknowledged, user []common.Address, provider []common.Address) (event.Subscription, error) {

	var userRule []interface{}
	for _, userItem := range user {
		userRule = append(userRule, userItem)
	}
	var providerRule []interface{}
	for _, providerItem := range provider {
		providerRule = append(providerRule, providerItem)
	}

	logs, sub, err := _SandboxServing.contract.WatchLogs(opts, "TEESignerAcknowledged", userRule, providerRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(SandboxServingTEESignerAcknowledged)
				if err := _SandboxServing.contract.UnpackLog(event, "TEESignerAcknowledged", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseTEESignerAcknowledged is a log parse operation binding the contract event 0x0002df5a9025c3e501b00d10c3bbfc3d8d37dbf5c904d758c9267a5a3880ee6f.
//
// Solidity: event TEESignerAcknowledged(address indexed user, address indexed provider, bool acknowledged)
func (_SandboxServing *SandboxServingFilterer) ParseTEESignerAcknowledged(log types.Log) (*SandboxServingTEESignerAcknowledged, error) {
	event := new(SandboxServingTEESignerAcknowledged)
	if err := _SandboxServing.contract.UnpackLog(event, "TEESignerAcknowledged", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// SandboxServingVoucherSettledIterator is returned from FilterVoucherSettled and is used to iterate over the raw logs and unpacked data for VoucherSettled events raised by the SandboxServing contract.
type SandboxServingVoucherSettledIterator struct {
	Event *SandboxServingVoucherSettled // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  ethereum.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *SandboxServingVoucherSettledIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(SandboxServingVoucherSettled)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(SandboxServingVoucherSettled)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *SandboxServingVoucherSettledIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *SandboxServingVoucherSettledIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// SandboxServingVoucherSettled represents a VoucherSettled event raised by the SandboxServing contract.
type SandboxServingVoucherSettled struct {
	User      common.Address
	Provider  common.Address
	TotalFee  *big.Int
	UsageHash [32]byte
	Nonce     *big.Int
	Status    uint8
	Raw       types.Log // Blockchain specific contextual infos
}

// FilterVoucherSettled is a free log retrieval operation binding the contract event 0x9ba6f66abeb399b035d74320c147acb495122d939be51ee8d92c43dae6daa77b.
//
// Solidity: event VoucherSettled(address indexed user, address indexed provider, uint256 totalFee, bytes32 usageHash, uint256 nonce, uint8 status)
func (_SandboxServing *SandboxServingFilterer) FilterVoucherSettled(opts *bind.FilterOpts, user []common.Address, provider []common.Address) (*SandboxServingVoucherSettledIterator, error) {

	var userRule []interface{}
	for _, userItem := range user {
		userRule = append(userRule, userItem)
	}
	var providerRule []interface{}
	for _, providerItem := range provider {
		providerRule = append(providerRule, providerItem)
	}

	logs, sub, err := _SandboxServing.contract.FilterLogs(opts, "VoucherSettled", userRule, providerRule)
	if err != nil {
		return nil, err
	}
	return &SandboxServingVoucherSettledIterator{contract: _SandboxServing.contract, event: "VoucherSettled", logs: logs, sub: sub}, nil
}

// WatchVoucherSettled is a free log subscription operation binding the contract event 0x9ba6f66abeb399b035d74320c147acb495122d939be51ee8d92c43dae6daa77b.
//
// Solidity: event VoucherSettled(address indexed user, address indexed provider, uint256 totalFee, bytes32 usageHash, uint256 nonce, uint8 status)
func (_SandboxServing *SandboxServingFilterer) WatchVoucherSettled(opts *bind.WatchOpts, sink chan<- *SandboxServingVoucherSettled, user []common.Address, provider []common.Address) (event.Subscription, error) {

	var userRule []interface{}
	for _, userItem := range user {
		userRule = append(userRule, userItem)
	}
	var providerRule []interface{}
	for _, providerItem := range provider {
		providerRule = append(providerRule, providerItem)
	}

	logs, sub, err := _SandboxServing.contract.WatchLogs(opts, "VoucherSettled", userRule, providerRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(SandboxServingVoucherSettled)
				if err := _SandboxServing.contract.UnpackLog(event, "VoucherSettled", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseVoucherSettled is a log parse operation binding the contract event 0x9ba6f66abeb399b035d74320c147acb495122d939be51ee8d92c43dae6daa77b.
//
// Solidity: event VoucherSettled(address indexed user, address indexed provider, uint256 totalFee, bytes32 usageHash, uint256 nonce, uint8 status)
func (_SandboxServing *SandboxServingFilterer) ParseVoucherSettled(log types.Log) (*SandboxServingVoucherSettled, error) {
	event := new(SandboxServingVoucherSettled)
	if err := _SandboxServing.contract.UnpackLog(event, "VoucherSettled", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}
