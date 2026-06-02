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

// TappRegistryAppInfo is an auto generated low-level Go binding around an user-defined struct.
type TappRegistryAppInfo struct {
	ComposeHash  []byte
	VolumesHash  []byte
	ImageHashes  [][]byte
	Owner        common.Address
	RegisteredAt *big.Int
}

// TappRegistryLockedEntry is an auto generated low-level Go binding around an user-defined struct.
type TappRegistryLockedEntry struct {
	Amount   *big.Int
	UnlockAt *big.Int
}

// TappRegistryNodeInfo is an auto generated low-level Go binding around an user-defined struct.
type TappRegistryNodeInfo struct {
	TeeUrl      string
	AddedAt     *big.Int
	StakeAmount *big.Int
}

// TappRegistryMetaData contains all meta data concerning the TappRegistry contract.
var TappRegistryMetaData = &bind.MetaData{
	ABI: "[{\"type\":\"constructor\",\"inputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"acknowledgeApp\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"internalType\":\"string\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"addNode\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"internalType\":\"string\"},{\"name\":\"signerAddress\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"teeUrl\",\"type\":\"string\",\"internalType\":\"string\"}],\"outputs\":[],\"stateMutability\":\"payable\"},{\"type\":\"function\",\"name\":\"admin\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"address\",\"internalType\":\"address\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"authorizeInvalidator\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"internalType\":\"string\"},{\"name\":\"invalidator\",\"type\":\"address\",\"internalType\":\"address\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"getAckCount\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"internalType\":\"string\"}],\"outputs\":[{\"name\":\"\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"getAckVersion\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"internalType\":\"string\"}],\"outputs\":[{\"name\":\"\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"getAppInfo\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"internalType\":\"string\"}],\"outputs\":[{\"name\":\"\",\"type\":\"tuple\",\"internalType\":\"structTappRegistry.AppInfo\",\"components\":[{\"name\":\"composeHash\",\"type\":\"bytes\",\"internalType\":\"bytes\"},{\"name\":\"volumesHash\",\"type\":\"bytes\",\"internalType\":\"bytes\"},{\"name\":\"imageHashes\",\"type\":\"bytes[]\",\"internalType\":\"bytes[]\"},{\"name\":\"owner\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"registeredAt\",\"type\":\"uint256\",\"internalType\":\"uint256\"}]}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"getLockedBalance\",\"inputs\":[{\"name\":\"owner\",\"type\":\"address\",\"internalType\":\"address\"}],\"outputs\":[{\"name\":\"\",\"type\":\"tuple[]\",\"internalType\":\"structTappRegistry.LockedEntry[]\",\"components\":[{\"name\":\"amount\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"unlockAt\",\"type\":\"uint256\",\"internalType\":\"uint256\"}]}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"getNode\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"internalType\":\"string\"},{\"name\":\"signerAddress\",\"type\":\"address\",\"internalType\":\"address\"}],\"outputs\":[{\"name\":\"\",\"type\":\"tuple\",\"internalType\":\"structTappRegistry.NodeInfo\",\"components\":[{\"name\":\"teeUrl\",\"type\":\"string\",\"internalType\":\"string\"},{\"name\":\"addedAt\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"stakeAmount\",\"type\":\"uint256\",\"internalType\":\"uint256\"}]}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"getNodeList\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"internalType\":\"string\"}],\"outputs\":[{\"name\":\"\",\"type\":\"address[]\",\"internalType\":\"address[]\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"initialize\",\"inputs\":[{\"name\":\"_minStakeAmount\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"_lockPeriod\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"invalidateAcks\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"internalType\":\"string\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"isAcknowledged\",\"inputs\":[{\"name\":\"user\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"appId\",\"type\":\"string\",\"internalType\":\"string\"}],\"outputs\":[{\"name\":\"\",\"type\":\"bool\",\"internalType\":\"bool\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"isAuthorizedInvalidator\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"internalType\":\"string\"},{\"name\":\"invalidator\",\"type\":\"address\",\"internalType\":\"address\"}],\"outputs\":[{\"name\":\"\",\"type\":\"bool\",\"internalType\":\"bool\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"lockPeriod\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"minStakeAmount\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"registerApp\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"internalType\":\"string\"},{\"name\":\"composeHash\",\"type\":\"bytes\",\"internalType\":\"bytes\"},{\"name\":\"volumesHash\",\"type\":\"bytes\",\"internalType\":\"bytes\"},{\"name\":\"imageHashes\",\"type\":\"bytes[]\",\"internalType\":\"bytes[]\"},{\"name\":\"firstSignerAddress\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"firstTeeUrl\",\"type\":\"string\",\"internalType\":\"string\"}],\"outputs\":[],\"stateMutability\":\"payable\"},{\"type\":\"function\",\"name\":\"removeNode\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"internalType\":\"string\"},{\"name\":\"signerAddress\",\"type\":\"address\",\"internalType\":\"address\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"revokeAcknowledgement\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"internalType\":\"string\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"revokeInvalidator\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"internalType\":\"string\"},{\"name\":\"invalidator\",\"type\":\"address\",\"internalType\":\"address\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"setLockPeriod\",\"inputs\":[{\"name\":\"period\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"setMinStakeAmount\",\"inputs\":[{\"name\":\"amount\",\"type\":\"uint256\",\"internalType\":\"uint256\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"transferAdmin\",\"inputs\":[{\"name\":\"newAdmin\",\"type\":\"address\",\"internalType\":\"address\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"updateApp\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"internalType\":\"string\"},{\"name\":\"composeHash\",\"type\":\"bytes\",\"internalType\":\"bytes\"},{\"name\":\"volumesHash\",\"type\":\"bytes\",\"internalType\":\"bytes\"},{\"name\":\"imageHashes\",\"type\":\"bytes[]\",\"internalType\":\"bytes[]\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"updateNode\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"internalType\":\"string\"},{\"name\":\"oldSigner\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"newSigner\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"teeUrl\",\"type\":\"string\",\"internalType\":\"string\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"withdraw\",\"inputs\":[],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"event\",\"name\":\"AcksInvalidated\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"indexed\":true,\"internalType\":\"string\"},{\"name\":\"invalidator\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"newAckVersion\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"AdminTransferred\",\"inputs\":[{\"name\":\"previousAdmin\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"newAdmin\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"AppAcknowledged\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"indexed\":true,\"internalType\":\"string\"},{\"name\":\"user\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"ackVersion\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"AppAcknowledgementRevoked\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"indexed\":true,\"internalType\":\"string\"},{\"name\":\"user\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"AppRegistered\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"indexed\":true,\"internalType\":\"string\"},{\"name\":\"owner\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"composeHash\",\"type\":\"bytes\",\"indexed\":false,\"internalType\":\"bytes\"},{\"name\":\"volumesHash\",\"type\":\"bytes\",\"indexed\":false,\"internalType\":\"bytes\"},{\"name\":\"imageHashes\",\"type\":\"bytes[]\",\"indexed\":false,\"internalType\":\"bytes[]\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"AppUnregistered\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"indexed\":true,\"internalType\":\"string\"},{\"name\":\"owner\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"AppUpdated\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"indexed\":true,\"internalType\":\"string\"},{\"name\":\"newAckVersion\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"},{\"name\":\"composeHash\",\"type\":\"bytes\",\"indexed\":false,\"internalType\":\"bytes\"},{\"name\":\"volumesHash\",\"type\":\"bytes\",\"indexed\":false,\"internalType\":\"bytes\"},{\"name\":\"imageHashes\",\"type\":\"bytes[]\",\"indexed\":false,\"internalType\":\"bytes[]\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"InvalidatorAuthorized\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"indexed\":true,\"internalType\":\"string\"},{\"name\":\"invalidator\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"InvalidatorRevoked\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"indexed\":true,\"internalType\":\"string\"},{\"name\":\"invalidator\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"LockPeriodUpdated\",\"inputs\":[{\"name\":\"oldPeriod\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"},{\"name\":\"newPeriod\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"MinStakeUpdated\",\"inputs\":[{\"name\":\"oldAmount\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"},{\"name\":\"newAmount\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"NodeUpdated\",\"inputs\":[{\"name\":\"appId\",\"type\":\"string\",\"indexed\":true,\"internalType\":\"string\"},{\"name\":\"oldSigner\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"newSigner\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"stakeAmount\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"},{\"name\":\"unlockAt\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"},{\"name\":\"newAckVersion\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"StakeWithdrawn\",\"inputs\":[{\"name\":\"owner\",\"type\":\"address\",\"indexed\":true,\"internalType\":\"address\"},{\"name\":\"amount\",\"type\":\"uint256\",\"indexed\":false,\"internalType\":\"uint256\"}],\"anonymous\":false}]",
}

// TappRegistryABI is the input ABI used to generate the binding from.
// Deprecated: Use TappRegistryMetaData.ABI instead.
var TappRegistryABI = TappRegistryMetaData.ABI

// TappRegistry is an auto generated Go binding around an Ethereum contract.
type TappRegistry struct {
	TappRegistryCaller     // Read-only binding to the contract
	TappRegistryTransactor // Write-only binding to the contract
	TappRegistryFilterer   // Log filterer for contract events
}

// TappRegistryCaller is an auto generated read-only Go binding around an Ethereum contract.
type TappRegistryCaller struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// TappRegistryTransactor is an auto generated write-only Go binding around an Ethereum contract.
type TappRegistryTransactor struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// TappRegistryFilterer is an auto generated log filtering Go binding around an Ethereum contract events.
type TappRegistryFilterer struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// TappRegistrySession is an auto generated Go binding around an Ethereum contract,
// with pre-set call and transact options.
type TappRegistrySession struct {
	Contract     *TappRegistry     // Generic contract binding to set the session for
	CallOpts     bind.CallOpts     // Call options to use throughout this session
	TransactOpts bind.TransactOpts // Transaction auth options to use throughout this session
}

// TappRegistryCallerSession is an auto generated read-only Go binding around an Ethereum contract,
// with pre-set call options.
type TappRegistryCallerSession struct {
	Contract *TappRegistryCaller // Generic contract caller binding to set the session for
	CallOpts bind.CallOpts       // Call options to use throughout this session
}

// TappRegistryTransactorSession is an auto generated write-only Go binding around an Ethereum contract,
// with pre-set transact options.
type TappRegistryTransactorSession struct {
	Contract     *TappRegistryTransactor // Generic contract transactor binding to set the session for
	TransactOpts bind.TransactOpts       // Transaction auth options to use throughout this session
}

// TappRegistryRaw is an auto generated low-level Go binding around an Ethereum contract.
type TappRegistryRaw struct {
	Contract *TappRegistry // Generic contract binding to access the raw methods on
}

// TappRegistryCallerRaw is an auto generated low-level read-only Go binding around an Ethereum contract.
type TappRegistryCallerRaw struct {
	Contract *TappRegistryCaller // Generic read-only contract binding to access the raw methods on
}

// TappRegistryTransactorRaw is an auto generated low-level write-only Go binding around an Ethereum contract.
type TappRegistryTransactorRaw struct {
	Contract *TappRegistryTransactor // Generic write-only contract binding to access the raw methods on
}

// NewTappRegistry creates a new instance of TappRegistry, bound to a specific deployed contract.
func NewTappRegistry(address common.Address, backend bind.ContractBackend) (*TappRegistry, error) {
	contract, err := bindTappRegistry(address, backend, backend, backend)
	if err != nil {
		return nil, err
	}
	return &TappRegistry{TappRegistryCaller: TappRegistryCaller{contract: contract}, TappRegistryTransactor: TappRegistryTransactor{contract: contract}, TappRegistryFilterer: TappRegistryFilterer{contract: contract}}, nil
}

// NewTappRegistryCaller creates a new read-only instance of TappRegistry, bound to a specific deployed contract.
func NewTappRegistryCaller(address common.Address, caller bind.ContractCaller) (*TappRegistryCaller, error) {
	contract, err := bindTappRegistry(address, caller, nil, nil)
	if err != nil {
		return nil, err
	}
	return &TappRegistryCaller{contract: contract}, nil
}

// NewTappRegistryTransactor creates a new write-only instance of TappRegistry, bound to a specific deployed contract.
func NewTappRegistryTransactor(address common.Address, transactor bind.ContractTransactor) (*TappRegistryTransactor, error) {
	contract, err := bindTappRegistry(address, nil, transactor, nil)
	if err != nil {
		return nil, err
	}
	return &TappRegistryTransactor{contract: contract}, nil
}

// NewTappRegistryFilterer creates a new log filterer instance of TappRegistry, bound to a specific deployed contract.
func NewTappRegistryFilterer(address common.Address, filterer bind.ContractFilterer) (*TappRegistryFilterer, error) {
	contract, err := bindTappRegistry(address, nil, nil, filterer)
	if err != nil {
		return nil, err
	}
	return &TappRegistryFilterer{contract: contract}, nil
}

// bindTappRegistry binds a generic wrapper to an already deployed contract.
func bindTappRegistry(address common.Address, caller bind.ContractCaller, transactor bind.ContractTransactor, filterer bind.ContractFilterer) (*bind.BoundContract, error) {
	parsed, err := TappRegistryMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	return bind.NewBoundContract(address, *parsed, caller, transactor, filterer), nil
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_TappRegistry *TappRegistryRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _TappRegistry.Contract.TappRegistryCaller.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_TappRegistry *TappRegistryRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _TappRegistry.Contract.TappRegistryTransactor.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_TappRegistry *TappRegistryRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _TappRegistry.Contract.TappRegistryTransactor.contract.Transact(opts, method, params...)
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_TappRegistry *TappRegistryCallerRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _TappRegistry.Contract.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_TappRegistry *TappRegistryTransactorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _TappRegistry.Contract.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_TappRegistry *TappRegistryTransactorRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _TappRegistry.Contract.contract.Transact(opts, method, params...)
}

// Admin is a free data retrieval call binding the contract method 0xf851a440.
//
// Solidity: function admin() view returns(address)
func (_TappRegistry *TappRegistryCaller) Admin(opts *bind.CallOpts) (common.Address, error) {
	var out []interface{}
	err := _TappRegistry.contract.Call(opts, &out, "admin")

	if err != nil {
		return *new(common.Address), err
	}

	out0 := *abi.ConvertType(out[0], new(common.Address)).(*common.Address)

	return out0, err

}

// Admin is a free data retrieval call binding the contract method 0xf851a440.
//
// Solidity: function admin() view returns(address)
func (_TappRegistry *TappRegistrySession) Admin() (common.Address, error) {
	return _TappRegistry.Contract.Admin(&_TappRegistry.CallOpts)
}

// Admin is a free data retrieval call binding the contract method 0xf851a440.
//
// Solidity: function admin() view returns(address)
func (_TappRegistry *TappRegistryCallerSession) Admin() (common.Address, error) {
	return _TappRegistry.Contract.Admin(&_TappRegistry.CallOpts)
}

// GetAckCount is a free data retrieval call binding the contract method 0x5848d3c0.
//
// Solidity: function getAckCount(string appId) view returns(uint256)
func (_TappRegistry *TappRegistryCaller) GetAckCount(opts *bind.CallOpts, appId string) (*big.Int, error) {
	var out []interface{}
	err := _TappRegistry.contract.Call(opts, &out, "getAckCount", appId)

	if err != nil {
		return *new(*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	return out0, err

}

// GetAckCount is a free data retrieval call binding the contract method 0x5848d3c0.
//
// Solidity: function getAckCount(string appId) view returns(uint256)
func (_TappRegistry *TappRegistrySession) GetAckCount(appId string) (*big.Int, error) {
	return _TappRegistry.Contract.GetAckCount(&_TappRegistry.CallOpts, appId)
}

// GetAckCount is a free data retrieval call binding the contract method 0x5848d3c0.
//
// Solidity: function getAckCount(string appId) view returns(uint256)
func (_TappRegistry *TappRegistryCallerSession) GetAckCount(appId string) (*big.Int, error) {
	return _TappRegistry.Contract.GetAckCount(&_TappRegistry.CallOpts, appId)
}

// GetAckVersion is a free data retrieval call binding the contract method 0x756d08e4.
//
// Solidity: function getAckVersion(string appId) view returns(uint256)
func (_TappRegistry *TappRegistryCaller) GetAckVersion(opts *bind.CallOpts, appId string) (*big.Int, error) {
	var out []interface{}
	err := _TappRegistry.contract.Call(opts, &out, "getAckVersion", appId)

	if err != nil {
		return *new(*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	return out0, err

}

// GetAckVersion is a free data retrieval call binding the contract method 0x756d08e4.
//
// Solidity: function getAckVersion(string appId) view returns(uint256)
func (_TappRegistry *TappRegistrySession) GetAckVersion(appId string) (*big.Int, error) {
	return _TappRegistry.Contract.GetAckVersion(&_TappRegistry.CallOpts, appId)
}

// GetAckVersion is a free data retrieval call binding the contract method 0x756d08e4.
//
// Solidity: function getAckVersion(string appId) view returns(uint256)
func (_TappRegistry *TappRegistryCallerSession) GetAckVersion(appId string) (*big.Int, error) {
	return _TappRegistry.Contract.GetAckVersion(&_TappRegistry.CallOpts, appId)
}

// GetAppInfo is a free data retrieval call binding the contract method 0x3acefa17.
//
// Solidity: function getAppInfo(string appId) view returns((bytes,bytes,bytes[],address,uint256))
func (_TappRegistry *TappRegistryCaller) GetAppInfo(opts *bind.CallOpts, appId string) (TappRegistryAppInfo, error) {
	var out []interface{}
	err := _TappRegistry.contract.Call(opts, &out, "getAppInfo", appId)

	if err != nil {
		return *new(TappRegistryAppInfo), err
	}

	out0 := *abi.ConvertType(out[0], new(TappRegistryAppInfo)).(*TappRegistryAppInfo)

	return out0, err

}

// GetAppInfo is a free data retrieval call binding the contract method 0x3acefa17.
//
// Solidity: function getAppInfo(string appId) view returns((bytes,bytes,bytes[],address,uint256))
func (_TappRegistry *TappRegistrySession) GetAppInfo(appId string) (TappRegistryAppInfo, error) {
	return _TappRegistry.Contract.GetAppInfo(&_TappRegistry.CallOpts, appId)
}

// GetAppInfo is a free data retrieval call binding the contract method 0x3acefa17.
//
// Solidity: function getAppInfo(string appId) view returns((bytes,bytes,bytes[],address,uint256))
func (_TappRegistry *TappRegistryCallerSession) GetAppInfo(appId string) (TappRegistryAppInfo, error) {
	return _TappRegistry.Contract.GetAppInfo(&_TappRegistry.CallOpts, appId)
}

// GetLockedBalance is a free data retrieval call binding the contract method 0xc4086893.
//
// Solidity: function getLockedBalance(address owner) view returns((uint256,uint256)[])
func (_TappRegistry *TappRegistryCaller) GetLockedBalance(opts *bind.CallOpts, owner common.Address) ([]TappRegistryLockedEntry, error) {
	var out []interface{}
	err := _TappRegistry.contract.Call(opts, &out, "getLockedBalance", owner)

	if err != nil {
		return *new([]TappRegistryLockedEntry), err
	}

	out0 := *abi.ConvertType(out[0], new([]TappRegistryLockedEntry)).(*[]TappRegistryLockedEntry)

	return out0, err

}

// GetLockedBalance is a free data retrieval call binding the contract method 0xc4086893.
//
// Solidity: function getLockedBalance(address owner) view returns((uint256,uint256)[])
func (_TappRegistry *TappRegistrySession) GetLockedBalance(owner common.Address) ([]TappRegistryLockedEntry, error) {
	return _TappRegistry.Contract.GetLockedBalance(&_TappRegistry.CallOpts, owner)
}

// GetLockedBalance is a free data retrieval call binding the contract method 0xc4086893.
//
// Solidity: function getLockedBalance(address owner) view returns((uint256,uint256)[])
func (_TappRegistry *TappRegistryCallerSession) GetLockedBalance(owner common.Address) ([]TappRegistryLockedEntry, error) {
	return _TappRegistry.Contract.GetLockedBalance(&_TappRegistry.CallOpts, owner)
}

// GetNode is a free data retrieval call binding the contract method 0xd711680c.
//
// Solidity: function getNode(string appId, address signerAddress) view returns((string,uint256,uint256))
func (_TappRegistry *TappRegistryCaller) GetNode(opts *bind.CallOpts, appId string, signerAddress common.Address) (TappRegistryNodeInfo, error) {
	var out []interface{}
	err := _TappRegistry.contract.Call(opts, &out, "getNode", appId, signerAddress)

	if err != nil {
		return *new(TappRegistryNodeInfo), err
	}

	out0 := *abi.ConvertType(out[0], new(TappRegistryNodeInfo)).(*TappRegistryNodeInfo)

	return out0, err

}

// GetNode is a free data retrieval call binding the contract method 0xd711680c.
//
// Solidity: function getNode(string appId, address signerAddress) view returns((string,uint256,uint256))
func (_TappRegistry *TappRegistrySession) GetNode(appId string, signerAddress common.Address) (TappRegistryNodeInfo, error) {
	return _TappRegistry.Contract.GetNode(&_TappRegistry.CallOpts, appId, signerAddress)
}

// GetNode is a free data retrieval call binding the contract method 0xd711680c.
//
// Solidity: function getNode(string appId, address signerAddress) view returns((string,uint256,uint256))
func (_TappRegistry *TappRegistryCallerSession) GetNode(appId string, signerAddress common.Address) (TappRegistryNodeInfo, error) {
	return _TappRegistry.Contract.GetNode(&_TappRegistry.CallOpts, appId, signerAddress)
}

// GetNodeList is a free data retrieval call binding the contract method 0x46e76d8b.
//
// Solidity: function getNodeList(string appId) view returns(address[])
func (_TappRegistry *TappRegistryCaller) GetNodeList(opts *bind.CallOpts, appId string) ([]common.Address, error) {
	var out []interface{}
	err := _TappRegistry.contract.Call(opts, &out, "getNodeList", appId)

	if err != nil {
		return *new([]common.Address), err
	}

	out0 := *abi.ConvertType(out[0], new([]common.Address)).(*[]common.Address)

	return out0, err

}

// GetNodeList is a free data retrieval call binding the contract method 0x46e76d8b.
//
// Solidity: function getNodeList(string appId) view returns(address[])
func (_TappRegistry *TappRegistrySession) GetNodeList(appId string) ([]common.Address, error) {
	return _TappRegistry.Contract.GetNodeList(&_TappRegistry.CallOpts, appId)
}

// GetNodeList is a free data retrieval call binding the contract method 0x46e76d8b.
//
// Solidity: function getNodeList(string appId) view returns(address[])
func (_TappRegistry *TappRegistryCallerSession) GetNodeList(appId string) ([]common.Address, error) {
	return _TappRegistry.Contract.GetNodeList(&_TappRegistry.CallOpts, appId)
}

// IsAcknowledged is a free data retrieval call binding the contract method 0x0daa2b8d.
//
// Solidity: function isAcknowledged(address user, string appId) view returns(bool)
func (_TappRegistry *TappRegistryCaller) IsAcknowledged(opts *bind.CallOpts, user common.Address, appId string) (bool, error) {
	var out []interface{}
	err := _TappRegistry.contract.Call(opts, &out, "isAcknowledged", user, appId)

	if err != nil {
		return *new(bool), err
	}

	out0 := *abi.ConvertType(out[0], new(bool)).(*bool)

	return out0, err

}

// IsAcknowledged is a free data retrieval call binding the contract method 0x0daa2b8d.
//
// Solidity: function isAcknowledged(address user, string appId) view returns(bool)
func (_TappRegistry *TappRegistrySession) IsAcknowledged(user common.Address, appId string) (bool, error) {
	return _TappRegistry.Contract.IsAcknowledged(&_TappRegistry.CallOpts, user, appId)
}

// IsAcknowledged is a free data retrieval call binding the contract method 0x0daa2b8d.
//
// Solidity: function isAcknowledged(address user, string appId) view returns(bool)
func (_TappRegistry *TappRegistryCallerSession) IsAcknowledged(user common.Address, appId string) (bool, error) {
	return _TappRegistry.Contract.IsAcknowledged(&_TappRegistry.CallOpts, user, appId)
}

// IsAuthorizedInvalidator is a free data retrieval call binding the contract method 0x5f9aa9d7.
//
// Solidity: function isAuthorizedInvalidator(string appId, address invalidator) view returns(bool)
func (_TappRegistry *TappRegistryCaller) IsAuthorizedInvalidator(opts *bind.CallOpts, appId string, invalidator common.Address) (bool, error) {
	var out []interface{}
	err := _TappRegistry.contract.Call(opts, &out, "isAuthorizedInvalidator", appId, invalidator)

	if err != nil {
		return *new(bool), err
	}

	out0 := *abi.ConvertType(out[0], new(bool)).(*bool)

	return out0, err

}

// IsAuthorizedInvalidator is a free data retrieval call binding the contract method 0x5f9aa9d7.
//
// Solidity: function isAuthorizedInvalidator(string appId, address invalidator) view returns(bool)
func (_TappRegistry *TappRegistrySession) IsAuthorizedInvalidator(appId string, invalidator common.Address) (bool, error) {
	return _TappRegistry.Contract.IsAuthorizedInvalidator(&_TappRegistry.CallOpts, appId, invalidator)
}

// IsAuthorizedInvalidator is a free data retrieval call binding the contract method 0x5f9aa9d7.
//
// Solidity: function isAuthorizedInvalidator(string appId, address invalidator) view returns(bool)
func (_TappRegistry *TappRegistryCallerSession) IsAuthorizedInvalidator(appId string, invalidator common.Address) (bool, error) {
	return _TappRegistry.Contract.IsAuthorizedInvalidator(&_TappRegistry.CallOpts, appId, invalidator)
}

// LockPeriod is a free data retrieval call binding the contract method 0x3fd8b02f.
//
// Solidity: function lockPeriod() view returns(uint256)
func (_TappRegistry *TappRegistryCaller) LockPeriod(opts *bind.CallOpts) (*big.Int, error) {
	var out []interface{}
	err := _TappRegistry.contract.Call(opts, &out, "lockPeriod")

	if err != nil {
		return *new(*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	return out0, err

}

// LockPeriod is a free data retrieval call binding the contract method 0x3fd8b02f.
//
// Solidity: function lockPeriod() view returns(uint256)
func (_TappRegistry *TappRegistrySession) LockPeriod() (*big.Int, error) {
	return _TappRegistry.Contract.LockPeriod(&_TappRegistry.CallOpts)
}

// LockPeriod is a free data retrieval call binding the contract method 0x3fd8b02f.
//
// Solidity: function lockPeriod() view returns(uint256)
func (_TappRegistry *TappRegistryCallerSession) LockPeriod() (*big.Int, error) {
	return _TappRegistry.Contract.LockPeriod(&_TappRegistry.CallOpts)
}

// MinStakeAmount is a free data retrieval call binding the contract method 0xf1887684.
//
// Solidity: function minStakeAmount() view returns(uint256)
func (_TappRegistry *TappRegistryCaller) MinStakeAmount(opts *bind.CallOpts) (*big.Int, error) {
	var out []interface{}
	err := _TappRegistry.contract.Call(opts, &out, "minStakeAmount")

	if err != nil {
		return *new(*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	return out0, err

}

// MinStakeAmount is a free data retrieval call binding the contract method 0xf1887684.
//
// Solidity: function minStakeAmount() view returns(uint256)
func (_TappRegistry *TappRegistrySession) MinStakeAmount() (*big.Int, error) {
	return _TappRegistry.Contract.MinStakeAmount(&_TappRegistry.CallOpts)
}

// MinStakeAmount is a free data retrieval call binding the contract method 0xf1887684.
//
// Solidity: function minStakeAmount() view returns(uint256)
func (_TappRegistry *TappRegistryCallerSession) MinStakeAmount() (*big.Int, error) {
	return _TappRegistry.Contract.MinStakeAmount(&_TappRegistry.CallOpts)
}

// AcknowledgeApp is a paid mutator transaction binding the contract method 0x3db63bb1.
//
// Solidity: function acknowledgeApp(string appId) returns()
func (_TappRegistry *TappRegistryTransactor) AcknowledgeApp(opts *bind.TransactOpts, appId string) (*types.Transaction, error) {
	return _TappRegistry.contract.Transact(opts, "acknowledgeApp", appId)
}

// AcknowledgeApp is a paid mutator transaction binding the contract method 0x3db63bb1.
//
// Solidity: function acknowledgeApp(string appId) returns()
func (_TappRegistry *TappRegistrySession) AcknowledgeApp(appId string) (*types.Transaction, error) {
	return _TappRegistry.Contract.AcknowledgeApp(&_TappRegistry.TransactOpts, appId)
}

// AcknowledgeApp is a paid mutator transaction binding the contract method 0x3db63bb1.
//
// Solidity: function acknowledgeApp(string appId) returns()
func (_TappRegistry *TappRegistryTransactorSession) AcknowledgeApp(appId string) (*types.Transaction, error) {
	return _TappRegistry.Contract.AcknowledgeApp(&_TappRegistry.TransactOpts, appId)
}

// AddNode is a paid mutator transaction binding the contract method 0x94c41558.
//
// Solidity: function addNode(string appId, address signerAddress, string teeUrl) payable returns()
func (_TappRegistry *TappRegistryTransactor) AddNode(opts *bind.TransactOpts, appId string, signerAddress common.Address, teeUrl string) (*types.Transaction, error) {
	return _TappRegistry.contract.Transact(opts, "addNode", appId, signerAddress, teeUrl)
}

// AddNode is a paid mutator transaction binding the contract method 0x94c41558.
//
// Solidity: function addNode(string appId, address signerAddress, string teeUrl) payable returns()
func (_TappRegistry *TappRegistrySession) AddNode(appId string, signerAddress common.Address, teeUrl string) (*types.Transaction, error) {
	return _TappRegistry.Contract.AddNode(&_TappRegistry.TransactOpts, appId, signerAddress, teeUrl)
}

// AddNode is a paid mutator transaction binding the contract method 0x94c41558.
//
// Solidity: function addNode(string appId, address signerAddress, string teeUrl) payable returns()
func (_TappRegistry *TappRegistryTransactorSession) AddNode(appId string, signerAddress common.Address, teeUrl string) (*types.Transaction, error) {
	return _TappRegistry.Contract.AddNode(&_TappRegistry.TransactOpts, appId, signerAddress, teeUrl)
}

// AuthorizeInvalidator is a paid mutator transaction binding the contract method 0x81730ead.
//
// Solidity: function authorizeInvalidator(string appId, address invalidator) returns()
func (_TappRegistry *TappRegistryTransactor) AuthorizeInvalidator(opts *bind.TransactOpts, appId string, invalidator common.Address) (*types.Transaction, error) {
	return _TappRegistry.contract.Transact(opts, "authorizeInvalidator", appId, invalidator)
}

// AuthorizeInvalidator is a paid mutator transaction binding the contract method 0x81730ead.
//
// Solidity: function authorizeInvalidator(string appId, address invalidator) returns()
func (_TappRegistry *TappRegistrySession) AuthorizeInvalidator(appId string, invalidator common.Address) (*types.Transaction, error) {
	return _TappRegistry.Contract.AuthorizeInvalidator(&_TappRegistry.TransactOpts, appId, invalidator)
}

// AuthorizeInvalidator is a paid mutator transaction binding the contract method 0x81730ead.
//
// Solidity: function authorizeInvalidator(string appId, address invalidator) returns()
func (_TappRegistry *TappRegistryTransactorSession) AuthorizeInvalidator(appId string, invalidator common.Address) (*types.Transaction, error) {
	return _TappRegistry.Contract.AuthorizeInvalidator(&_TappRegistry.TransactOpts, appId, invalidator)
}

// Initialize is a paid mutator transaction binding the contract method 0xe4a30116.
//
// Solidity: function initialize(uint256 _minStakeAmount, uint256 _lockPeriod) returns()
func (_TappRegistry *TappRegistryTransactor) Initialize(opts *bind.TransactOpts, _minStakeAmount *big.Int, _lockPeriod *big.Int) (*types.Transaction, error) {
	return _TappRegistry.contract.Transact(opts, "initialize", _minStakeAmount, _lockPeriod)
}

// Initialize is a paid mutator transaction binding the contract method 0xe4a30116.
//
// Solidity: function initialize(uint256 _minStakeAmount, uint256 _lockPeriod) returns()
func (_TappRegistry *TappRegistrySession) Initialize(_minStakeAmount *big.Int, _lockPeriod *big.Int) (*types.Transaction, error) {
	return _TappRegistry.Contract.Initialize(&_TappRegistry.TransactOpts, _minStakeAmount, _lockPeriod)
}

// Initialize is a paid mutator transaction binding the contract method 0xe4a30116.
//
// Solidity: function initialize(uint256 _minStakeAmount, uint256 _lockPeriod) returns()
func (_TappRegistry *TappRegistryTransactorSession) Initialize(_minStakeAmount *big.Int, _lockPeriod *big.Int) (*types.Transaction, error) {
	return _TappRegistry.Contract.Initialize(&_TappRegistry.TransactOpts, _minStakeAmount, _lockPeriod)
}

// InvalidateAcks is a paid mutator transaction binding the contract method 0x3f16e14f.
//
// Solidity: function invalidateAcks(string appId) returns()
func (_TappRegistry *TappRegistryTransactor) InvalidateAcks(opts *bind.TransactOpts, appId string) (*types.Transaction, error) {
	return _TappRegistry.contract.Transact(opts, "invalidateAcks", appId)
}

// InvalidateAcks is a paid mutator transaction binding the contract method 0x3f16e14f.
//
// Solidity: function invalidateAcks(string appId) returns()
func (_TappRegistry *TappRegistrySession) InvalidateAcks(appId string) (*types.Transaction, error) {
	return _TappRegistry.Contract.InvalidateAcks(&_TappRegistry.TransactOpts, appId)
}

// InvalidateAcks is a paid mutator transaction binding the contract method 0x3f16e14f.
//
// Solidity: function invalidateAcks(string appId) returns()
func (_TappRegistry *TappRegistryTransactorSession) InvalidateAcks(appId string) (*types.Transaction, error) {
	return _TappRegistry.Contract.InvalidateAcks(&_TappRegistry.TransactOpts, appId)
}

// RegisterApp is a paid mutator transaction binding the contract method 0x9e77e271.
//
// Solidity: function registerApp(string appId, bytes composeHash, bytes volumesHash, bytes[] imageHashes, address firstSignerAddress, string firstTeeUrl) payable returns()
func (_TappRegistry *TappRegistryTransactor) RegisterApp(opts *bind.TransactOpts, appId string, composeHash []byte, volumesHash []byte, imageHashes [][]byte, firstSignerAddress common.Address, firstTeeUrl string) (*types.Transaction, error) {
	return _TappRegistry.contract.Transact(opts, "registerApp", appId, composeHash, volumesHash, imageHashes, firstSignerAddress, firstTeeUrl)
}

// RegisterApp is a paid mutator transaction binding the contract method 0x9e77e271.
//
// Solidity: function registerApp(string appId, bytes composeHash, bytes volumesHash, bytes[] imageHashes, address firstSignerAddress, string firstTeeUrl) payable returns()
func (_TappRegistry *TappRegistrySession) RegisterApp(appId string, composeHash []byte, volumesHash []byte, imageHashes [][]byte, firstSignerAddress common.Address, firstTeeUrl string) (*types.Transaction, error) {
	return _TappRegistry.Contract.RegisterApp(&_TappRegistry.TransactOpts, appId, composeHash, volumesHash, imageHashes, firstSignerAddress, firstTeeUrl)
}

// RegisterApp is a paid mutator transaction binding the contract method 0x9e77e271.
//
// Solidity: function registerApp(string appId, bytes composeHash, bytes volumesHash, bytes[] imageHashes, address firstSignerAddress, string firstTeeUrl) payable returns()
func (_TappRegistry *TappRegistryTransactorSession) RegisterApp(appId string, composeHash []byte, volumesHash []byte, imageHashes [][]byte, firstSignerAddress common.Address, firstTeeUrl string) (*types.Transaction, error) {
	return _TappRegistry.Contract.RegisterApp(&_TappRegistry.TransactOpts, appId, composeHash, volumesHash, imageHashes, firstSignerAddress, firstTeeUrl)
}

// RemoveNode is a paid mutator transaction binding the contract method 0x6cf02bea.
//
// Solidity: function removeNode(string appId, address signerAddress) returns()
func (_TappRegistry *TappRegistryTransactor) RemoveNode(opts *bind.TransactOpts, appId string, signerAddress common.Address) (*types.Transaction, error) {
	return _TappRegistry.contract.Transact(opts, "removeNode", appId, signerAddress)
}

// RemoveNode is a paid mutator transaction binding the contract method 0x6cf02bea.
//
// Solidity: function removeNode(string appId, address signerAddress) returns()
func (_TappRegistry *TappRegistrySession) RemoveNode(appId string, signerAddress common.Address) (*types.Transaction, error) {
	return _TappRegistry.Contract.RemoveNode(&_TappRegistry.TransactOpts, appId, signerAddress)
}

// RemoveNode is a paid mutator transaction binding the contract method 0x6cf02bea.
//
// Solidity: function removeNode(string appId, address signerAddress) returns()
func (_TappRegistry *TappRegistryTransactorSession) RemoveNode(appId string, signerAddress common.Address) (*types.Transaction, error) {
	return _TappRegistry.Contract.RemoveNode(&_TappRegistry.TransactOpts, appId, signerAddress)
}

// RevokeAcknowledgement is a paid mutator transaction binding the contract method 0xc0b90934.
//
// Solidity: function revokeAcknowledgement(string appId) returns()
func (_TappRegistry *TappRegistryTransactor) RevokeAcknowledgement(opts *bind.TransactOpts, appId string) (*types.Transaction, error) {
	return _TappRegistry.contract.Transact(opts, "revokeAcknowledgement", appId)
}

// RevokeAcknowledgement is a paid mutator transaction binding the contract method 0xc0b90934.
//
// Solidity: function revokeAcknowledgement(string appId) returns()
func (_TappRegistry *TappRegistrySession) RevokeAcknowledgement(appId string) (*types.Transaction, error) {
	return _TappRegistry.Contract.RevokeAcknowledgement(&_TappRegistry.TransactOpts, appId)
}

// RevokeAcknowledgement is a paid mutator transaction binding the contract method 0xc0b90934.
//
// Solidity: function revokeAcknowledgement(string appId) returns()
func (_TappRegistry *TappRegistryTransactorSession) RevokeAcknowledgement(appId string) (*types.Transaction, error) {
	return _TappRegistry.Contract.RevokeAcknowledgement(&_TappRegistry.TransactOpts, appId)
}

// RevokeInvalidator is a paid mutator transaction binding the contract method 0xd4fb97cf.
//
// Solidity: function revokeInvalidator(string appId, address invalidator) returns()
func (_TappRegistry *TappRegistryTransactor) RevokeInvalidator(opts *bind.TransactOpts, appId string, invalidator common.Address) (*types.Transaction, error) {
	return _TappRegistry.contract.Transact(opts, "revokeInvalidator", appId, invalidator)
}

// RevokeInvalidator is a paid mutator transaction binding the contract method 0xd4fb97cf.
//
// Solidity: function revokeInvalidator(string appId, address invalidator) returns()
func (_TappRegistry *TappRegistrySession) RevokeInvalidator(appId string, invalidator common.Address) (*types.Transaction, error) {
	return _TappRegistry.Contract.RevokeInvalidator(&_TappRegistry.TransactOpts, appId, invalidator)
}

// RevokeInvalidator is a paid mutator transaction binding the contract method 0xd4fb97cf.
//
// Solidity: function revokeInvalidator(string appId, address invalidator) returns()
func (_TappRegistry *TappRegistryTransactorSession) RevokeInvalidator(appId string, invalidator common.Address) (*types.Transaction, error) {
	return _TappRegistry.Contract.RevokeInvalidator(&_TappRegistry.TransactOpts, appId, invalidator)
}

// SetLockPeriod is a paid mutator transaction binding the contract method 0x779972da.
//
// Solidity: function setLockPeriod(uint256 period) returns()
func (_TappRegistry *TappRegistryTransactor) SetLockPeriod(opts *bind.TransactOpts, period *big.Int) (*types.Transaction, error) {
	return _TappRegistry.contract.Transact(opts, "setLockPeriod", period)
}

// SetLockPeriod is a paid mutator transaction binding the contract method 0x779972da.
//
// Solidity: function setLockPeriod(uint256 period) returns()
func (_TappRegistry *TappRegistrySession) SetLockPeriod(period *big.Int) (*types.Transaction, error) {
	return _TappRegistry.Contract.SetLockPeriod(&_TappRegistry.TransactOpts, period)
}

// SetLockPeriod is a paid mutator transaction binding the contract method 0x779972da.
//
// Solidity: function setLockPeriod(uint256 period) returns()
func (_TappRegistry *TappRegistryTransactorSession) SetLockPeriod(period *big.Int) (*types.Transaction, error) {
	return _TappRegistry.Contract.SetLockPeriod(&_TappRegistry.TransactOpts, period)
}

// SetMinStakeAmount is a paid mutator transaction binding the contract method 0xeb4af045.
//
// Solidity: function setMinStakeAmount(uint256 amount) returns()
func (_TappRegistry *TappRegistryTransactor) SetMinStakeAmount(opts *bind.TransactOpts, amount *big.Int) (*types.Transaction, error) {
	return _TappRegistry.contract.Transact(opts, "setMinStakeAmount", amount)
}

// SetMinStakeAmount is a paid mutator transaction binding the contract method 0xeb4af045.
//
// Solidity: function setMinStakeAmount(uint256 amount) returns()
func (_TappRegistry *TappRegistrySession) SetMinStakeAmount(amount *big.Int) (*types.Transaction, error) {
	return _TappRegistry.Contract.SetMinStakeAmount(&_TappRegistry.TransactOpts, amount)
}

// SetMinStakeAmount is a paid mutator transaction binding the contract method 0xeb4af045.
//
// Solidity: function setMinStakeAmount(uint256 amount) returns()
func (_TappRegistry *TappRegistryTransactorSession) SetMinStakeAmount(amount *big.Int) (*types.Transaction, error) {
	return _TappRegistry.Contract.SetMinStakeAmount(&_TappRegistry.TransactOpts, amount)
}

// TransferAdmin is a paid mutator transaction binding the contract method 0x75829def.
//
// Solidity: function transferAdmin(address newAdmin) returns()
func (_TappRegistry *TappRegistryTransactor) TransferAdmin(opts *bind.TransactOpts, newAdmin common.Address) (*types.Transaction, error) {
	return _TappRegistry.contract.Transact(opts, "transferAdmin", newAdmin)
}

// TransferAdmin is a paid mutator transaction binding the contract method 0x75829def.
//
// Solidity: function transferAdmin(address newAdmin) returns()
func (_TappRegistry *TappRegistrySession) TransferAdmin(newAdmin common.Address) (*types.Transaction, error) {
	return _TappRegistry.Contract.TransferAdmin(&_TappRegistry.TransactOpts, newAdmin)
}

// TransferAdmin is a paid mutator transaction binding the contract method 0x75829def.
//
// Solidity: function transferAdmin(address newAdmin) returns()
func (_TappRegistry *TappRegistryTransactorSession) TransferAdmin(newAdmin common.Address) (*types.Transaction, error) {
	return _TappRegistry.Contract.TransferAdmin(&_TappRegistry.TransactOpts, newAdmin)
}

// UpdateApp is a paid mutator transaction binding the contract method 0x7614b8dd.
//
// Solidity: function updateApp(string appId, bytes composeHash, bytes volumesHash, bytes[] imageHashes) returns()
func (_TappRegistry *TappRegistryTransactor) UpdateApp(opts *bind.TransactOpts, appId string, composeHash []byte, volumesHash []byte, imageHashes [][]byte) (*types.Transaction, error) {
	return _TappRegistry.contract.Transact(opts, "updateApp", appId, composeHash, volumesHash, imageHashes)
}

// UpdateApp is a paid mutator transaction binding the contract method 0x7614b8dd.
//
// Solidity: function updateApp(string appId, bytes composeHash, bytes volumesHash, bytes[] imageHashes) returns()
func (_TappRegistry *TappRegistrySession) UpdateApp(appId string, composeHash []byte, volumesHash []byte, imageHashes [][]byte) (*types.Transaction, error) {
	return _TappRegistry.Contract.UpdateApp(&_TappRegistry.TransactOpts, appId, composeHash, volumesHash, imageHashes)
}

// UpdateApp is a paid mutator transaction binding the contract method 0x7614b8dd.
//
// Solidity: function updateApp(string appId, bytes composeHash, bytes volumesHash, bytes[] imageHashes) returns()
func (_TappRegistry *TappRegistryTransactorSession) UpdateApp(appId string, composeHash []byte, volumesHash []byte, imageHashes [][]byte) (*types.Transaction, error) {
	return _TappRegistry.Contract.UpdateApp(&_TappRegistry.TransactOpts, appId, composeHash, volumesHash, imageHashes)
}

// UpdateNode is a paid mutator transaction binding the contract method 0xfd1ef3cc.
//
// Solidity: function updateNode(string appId, address oldSigner, address newSigner, string teeUrl) returns()
func (_TappRegistry *TappRegistryTransactor) UpdateNode(opts *bind.TransactOpts, appId string, oldSigner common.Address, newSigner common.Address, teeUrl string) (*types.Transaction, error) {
	return _TappRegistry.contract.Transact(opts, "updateNode", appId, oldSigner, newSigner, teeUrl)
}

// UpdateNode is a paid mutator transaction binding the contract method 0xfd1ef3cc.
//
// Solidity: function updateNode(string appId, address oldSigner, address newSigner, string teeUrl) returns()
func (_TappRegistry *TappRegistrySession) UpdateNode(appId string, oldSigner common.Address, newSigner common.Address, teeUrl string) (*types.Transaction, error) {
	return _TappRegistry.Contract.UpdateNode(&_TappRegistry.TransactOpts, appId, oldSigner, newSigner, teeUrl)
}

// UpdateNode is a paid mutator transaction binding the contract method 0xfd1ef3cc.
//
// Solidity: function updateNode(string appId, address oldSigner, address newSigner, string teeUrl) returns()
func (_TappRegistry *TappRegistryTransactorSession) UpdateNode(appId string, oldSigner common.Address, newSigner common.Address, teeUrl string) (*types.Transaction, error) {
	return _TappRegistry.Contract.UpdateNode(&_TappRegistry.TransactOpts, appId, oldSigner, newSigner, teeUrl)
}

// Withdraw is a paid mutator transaction binding the contract method 0x3ccfd60b.
//
// Solidity: function withdraw() returns()
func (_TappRegistry *TappRegistryTransactor) Withdraw(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _TappRegistry.contract.Transact(opts, "withdraw")
}

// Withdraw is a paid mutator transaction binding the contract method 0x3ccfd60b.
//
// Solidity: function withdraw() returns()
func (_TappRegistry *TappRegistrySession) Withdraw() (*types.Transaction, error) {
	return _TappRegistry.Contract.Withdraw(&_TappRegistry.TransactOpts)
}

// Withdraw is a paid mutator transaction binding the contract method 0x3ccfd60b.
//
// Solidity: function withdraw() returns()
func (_TappRegistry *TappRegistryTransactorSession) Withdraw() (*types.Transaction, error) {
	return _TappRegistry.Contract.Withdraw(&_TappRegistry.TransactOpts)
}

// TappRegistryAcksInvalidatedIterator is returned from FilterAcksInvalidated and is used to iterate over the raw logs and unpacked data for AcksInvalidated events raised by the TappRegistry contract.
type TappRegistryAcksInvalidatedIterator struct {
	Event *TappRegistryAcksInvalidated // Event containing the contract specifics and raw log

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
func (it *TappRegistryAcksInvalidatedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(TappRegistryAcksInvalidated)
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
		it.Event = new(TappRegistryAcksInvalidated)
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
func (it *TappRegistryAcksInvalidatedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *TappRegistryAcksInvalidatedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// TappRegistryAcksInvalidated represents a AcksInvalidated event raised by the TappRegistry contract.
type TappRegistryAcksInvalidated struct {
	AppId         common.Hash
	Invalidator   common.Address
	NewAckVersion *big.Int
	Raw           types.Log // Blockchain specific contextual infos
}

// FilterAcksInvalidated is a free log retrieval operation binding the contract event 0xcb7a9ca7e970145f9dc07e16074927bb8cc1a0bde6aacf3bb22e548b60580b82.
//
// Solidity: event AcksInvalidated(string indexed appId, address indexed invalidator, uint256 newAckVersion)
func (_TappRegistry *TappRegistryFilterer) FilterAcksInvalidated(opts *bind.FilterOpts, appId []string, invalidator []common.Address) (*TappRegistryAcksInvalidatedIterator, error) {

	var appIdRule []interface{}
	for _, appIdItem := range appId {
		appIdRule = append(appIdRule, appIdItem)
	}
	var invalidatorRule []interface{}
	for _, invalidatorItem := range invalidator {
		invalidatorRule = append(invalidatorRule, invalidatorItem)
	}

	logs, sub, err := _TappRegistry.contract.FilterLogs(opts, "AcksInvalidated", appIdRule, invalidatorRule)
	if err != nil {
		return nil, err
	}
	return &TappRegistryAcksInvalidatedIterator{contract: _TappRegistry.contract, event: "AcksInvalidated", logs: logs, sub: sub}, nil
}

// WatchAcksInvalidated is a free log subscription operation binding the contract event 0xcb7a9ca7e970145f9dc07e16074927bb8cc1a0bde6aacf3bb22e548b60580b82.
//
// Solidity: event AcksInvalidated(string indexed appId, address indexed invalidator, uint256 newAckVersion)
func (_TappRegistry *TappRegistryFilterer) WatchAcksInvalidated(opts *bind.WatchOpts, sink chan<- *TappRegistryAcksInvalidated, appId []string, invalidator []common.Address) (event.Subscription, error) {

	var appIdRule []interface{}
	for _, appIdItem := range appId {
		appIdRule = append(appIdRule, appIdItem)
	}
	var invalidatorRule []interface{}
	for _, invalidatorItem := range invalidator {
		invalidatorRule = append(invalidatorRule, invalidatorItem)
	}

	logs, sub, err := _TappRegistry.contract.WatchLogs(opts, "AcksInvalidated", appIdRule, invalidatorRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(TappRegistryAcksInvalidated)
				if err := _TappRegistry.contract.UnpackLog(event, "AcksInvalidated", log); err != nil {
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

// ParseAcksInvalidated is a log parse operation binding the contract event 0xcb7a9ca7e970145f9dc07e16074927bb8cc1a0bde6aacf3bb22e548b60580b82.
//
// Solidity: event AcksInvalidated(string indexed appId, address indexed invalidator, uint256 newAckVersion)
func (_TappRegistry *TappRegistryFilterer) ParseAcksInvalidated(log types.Log) (*TappRegistryAcksInvalidated, error) {
	event := new(TappRegistryAcksInvalidated)
	if err := _TappRegistry.contract.UnpackLog(event, "AcksInvalidated", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// TappRegistryAdminTransferredIterator is returned from FilterAdminTransferred and is used to iterate over the raw logs and unpacked data for AdminTransferred events raised by the TappRegistry contract.
type TappRegistryAdminTransferredIterator struct {
	Event *TappRegistryAdminTransferred // Event containing the contract specifics and raw log

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
func (it *TappRegistryAdminTransferredIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(TappRegistryAdminTransferred)
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
		it.Event = new(TappRegistryAdminTransferred)
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
func (it *TappRegistryAdminTransferredIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *TappRegistryAdminTransferredIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// TappRegistryAdminTransferred represents a AdminTransferred event raised by the TappRegistry contract.
type TappRegistryAdminTransferred struct {
	PreviousAdmin common.Address
	NewAdmin      common.Address
	Raw           types.Log // Blockchain specific contextual infos
}

// FilterAdminTransferred is a free log retrieval operation binding the contract event 0xf8ccb027dfcd135e000e9d45e6cc2d662578a8825d4c45b5e32e0adf67e79ec6.
//
// Solidity: event AdminTransferred(address indexed previousAdmin, address indexed newAdmin)
func (_TappRegistry *TappRegistryFilterer) FilterAdminTransferred(opts *bind.FilterOpts, previousAdmin []common.Address, newAdmin []common.Address) (*TappRegistryAdminTransferredIterator, error) {

	var previousAdminRule []interface{}
	for _, previousAdminItem := range previousAdmin {
		previousAdminRule = append(previousAdminRule, previousAdminItem)
	}
	var newAdminRule []interface{}
	for _, newAdminItem := range newAdmin {
		newAdminRule = append(newAdminRule, newAdminItem)
	}

	logs, sub, err := _TappRegistry.contract.FilterLogs(opts, "AdminTransferred", previousAdminRule, newAdminRule)
	if err != nil {
		return nil, err
	}
	return &TappRegistryAdminTransferredIterator{contract: _TappRegistry.contract, event: "AdminTransferred", logs: logs, sub: sub}, nil
}

// WatchAdminTransferred is a free log subscription operation binding the contract event 0xf8ccb027dfcd135e000e9d45e6cc2d662578a8825d4c45b5e32e0adf67e79ec6.
//
// Solidity: event AdminTransferred(address indexed previousAdmin, address indexed newAdmin)
func (_TappRegistry *TappRegistryFilterer) WatchAdminTransferred(opts *bind.WatchOpts, sink chan<- *TappRegistryAdminTransferred, previousAdmin []common.Address, newAdmin []common.Address) (event.Subscription, error) {

	var previousAdminRule []interface{}
	for _, previousAdminItem := range previousAdmin {
		previousAdminRule = append(previousAdminRule, previousAdminItem)
	}
	var newAdminRule []interface{}
	for _, newAdminItem := range newAdmin {
		newAdminRule = append(newAdminRule, newAdminItem)
	}

	logs, sub, err := _TappRegistry.contract.WatchLogs(opts, "AdminTransferred", previousAdminRule, newAdminRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(TappRegistryAdminTransferred)
				if err := _TappRegistry.contract.UnpackLog(event, "AdminTransferred", log); err != nil {
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

// ParseAdminTransferred is a log parse operation binding the contract event 0xf8ccb027dfcd135e000e9d45e6cc2d662578a8825d4c45b5e32e0adf67e79ec6.
//
// Solidity: event AdminTransferred(address indexed previousAdmin, address indexed newAdmin)
func (_TappRegistry *TappRegistryFilterer) ParseAdminTransferred(log types.Log) (*TappRegistryAdminTransferred, error) {
	event := new(TappRegistryAdminTransferred)
	if err := _TappRegistry.contract.UnpackLog(event, "AdminTransferred", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// TappRegistryAppAcknowledgedIterator is returned from FilterAppAcknowledged and is used to iterate over the raw logs and unpacked data for AppAcknowledged events raised by the TappRegistry contract.
type TappRegistryAppAcknowledgedIterator struct {
	Event *TappRegistryAppAcknowledged // Event containing the contract specifics and raw log

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
func (it *TappRegistryAppAcknowledgedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(TappRegistryAppAcknowledged)
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
		it.Event = new(TappRegistryAppAcknowledged)
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
func (it *TappRegistryAppAcknowledgedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *TappRegistryAppAcknowledgedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// TappRegistryAppAcknowledged represents a AppAcknowledged event raised by the TappRegistry contract.
type TappRegistryAppAcknowledged struct {
	AppId      common.Hash
	User       common.Address
	AckVersion *big.Int
	Raw        types.Log // Blockchain specific contextual infos
}

// FilterAppAcknowledged is a free log retrieval operation binding the contract event 0x23b4b358ffc8a6cac9fd8018aa0999e7ff53e04988a0affcba4c471be3a71ec4.
//
// Solidity: event AppAcknowledged(string indexed appId, address indexed user, uint256 ackVersion)
func (_TappRegistry *TappRegistryFilterer) FilterAppAcknowledged(opts *bind.FilterOpts, appId []string, user []common.Address) (*TappRegistryAppAcknowledgedIterator, error) {

	var appIdRule []interface{}
	for _, appIdItem := range appId {
		appIdRule = append(appIdRule, appIdItem)
	}
	var userRule []interface{}
	for _, userItem := range user {
		userRule = append(userRule, userItem)
	}

	logs, sub, err := _TappRegistry.contract.FilterLogs(opts, "AppAcknowledged", appIdRule, userRule)
	if err != nil {
		return nil, err
	}
	return &TappRegistryAppAcknowledgedIterator{contract: _TappRegistry.contract, event: "AppAcknowledged", logs: logs, sub: sub}, nil
}

// WatchAppAcknowledged is a free log subscription operation binding the contract event 0x23b4b358ffc8a6cac9fd8018aa0999e7ff53e04988a0affcba4c471be3a71ec4.
//
// Solidity: event AppAcknowledged(string indexed appId, address indexed user, uint256 ackVersion)
func (_TappRegistry *TappRegistryFilterer) WatchAppAcknowledged(opts *bind.WatchOpts, sink chan<- *TappRegistryAppAcknowledged, appId []string, user []common.Address) (event.Subscription, error) {

	var appIdRule []interface{}
	for _, appIdItem := range appId {
		appIdRule = append(appIdRule, appIdItem)
	}
	var userRule []interface{}
	for _, userItem := range user {
		userRule = append(userRule, userItem)
	}

	logs, sub, err := _TappRegistry.contract.WatchLogs(opts, "AppAcknowledged", appIdRule, userRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(TappRegistryAppAcknowledged)
				if err := _TappRegistry.contract.UnpackLog(event, "AppAcknowledged", log); err != nil {
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

// ParseAppAcknowledged is a log parse operation binding the contract event 0x23b4b358ffc8a6cac9fd8018aa0999e7ff53e04988a0affcba4c471be3a71ec4.
//
// Solidity: event AppAcknowledged(string indexed appId, address indexed user, uint256 ackVersion)
func (_TappRegistry *TappRegistryFilterer) ParseAppAcknowledged(log types.Log) (*TappRegistryAppAcknowledged, error) {
	event := new(TappRegistryAppAcknowledged)
	if err := _TappRegistry.contract.UnpackLog(event, "AppAcknowledged", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// TappRegistryAppAcknowledgementRevokedIterator is returned from FilterAppAcknowledgementRevoked and is used to iterate over the raw logs and unpacked data for AppAcknowledgementRevoked events raised by the TappRegistry contract.
type TappRegistryAppAcknowledgementRevokedIterator struct {
	Event *TappRegistryAppAcknowledgementRevoked // Event containing the contract specifics and raw log

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
func (it *TappRegistryAppAcknowledgementRevokedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(TappRegistryAppAcknowledgementRevoked)
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
		it.Event = new(TappRegistryAppAcknowledgementRevoked)
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
func (it *TappRegistryAppAcknowledgementRevokedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *TappRegistryAppAcknowledgementRevokedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// TappRegistryAppAcknowledgementRevoked represents a AppAcknowledgementRevoked event raised by the TappRegistry contract.
type TappRegistryAppAcknowledgementRevoked struct {
	AppId common.Hash
	User  common.Address
	Raw   types.Log // Blockchain specific contextual infos
}

// FilterAppAcknowledgementRevoked is a free log retrieval operation binding the contract event 0xc2f284e9626d7417bec2b0497afe262b53bc65cbe1c4d4bfa3af692c14af9124.
//
// Solidity: event AppAcknowledgementRevoked(string indexed appId, address indexed user)
func (_TappRegistry *TappRegistryFilterer) FilterAppAcknowledgementRevoked(opts *bind.FilterOpts, appId []string, user []common.Address) (*TappRegistryAppAcknowledgementRevokedIterator, error) {

	var appIdRule []interface{}
	for _, appIdItem := range appId {
		appIdRule = append(appIdRule, appIdItem)
	}
	var userRule []interface{}
	for _, userItem := range user {
		userRule = append(userRule, userItem)
	}

	logs, sub, err := _TappRegistry.contract.FilterLogs(opts, "AppAcknowledgementRevoked", appIdRule, userRule)
	if err != nil {
		return nil, err
	}
	return &TappRegistryAppAcknowledgementRevokedIterator{contract: _TappRegistry.contract, event: "AppAcknowledgementRevoked", logs: logs, sub: sub}, nil
}

// WatchAppAcknowledgementRevoked is a free log subscription operation binding the contract event 0xc2f284e9626d7417bec2b0497afe262b53bc65cbe1c4d4bfa3af692c14af9124.
//
// Solidity: event AppAcknowledgementRevoked(string indexed appId, address indexed user)
func (_TappRegistry *TappRegistryFilterer) WatchAppAcknowledgementRevoked(opts *bind.WatchOpts, sink chan<- *TappRegistryAppAcknowledgementRevoked, appId []string, user []common.Address) (event.Subscription, error) {

	var appIdRule []interface{}
	for _, appIdItem := range appId {
		appIdRule = append(appIdRule, appIdItem)
	}
	var userRule []interface{}
	for _, userItem := range user {
		userRule = append(userRule, userItem)
	}

	logs, sub, err := _TappRegistry.contract.WatchLogs(opts, "AppAcknowledgementRevoked", appIdRule, userRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(TappRegistryAppAcknowledgementRevoked)
				if err := _TappRegistry.contract.UnpackLog(event, "AppAcknowledgementRevoked", log); err != nil {
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

// ParseAppAcknowledgementRevoked is a log parse operation binding the contract event 0xc2f284e9626d7417bec2b0497afe262b53bc65cbe1c4d4bfa3af692c14af9124.
//
// Solidity: event AppAcknowledgementRevoked(string indexed appId, address indexed user)
func (_TappRegistry *TappRegistryFilterer) ParseAppAcknowledgementRevoked(log types.Log) (*TappRegistryAppAcknowledgementRevoked, error) {
	event := new(TappRegistryAppAcknowledgementRevoked)
	if err := _TappRegistry.contract.UnpackLog(event, "AppAcknowledgementRevoked", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// TappRegistryAppRegisteredIterator is returned from FilterAppRegistered and is used to iterate over the raw logs and unpacked data for AppRegistered events raised by the TappRegistry contract.
type TappRegistryAppRegisteredIterator struct {
	Event *TappRegistryAppRegistered // Event containing the contract specifics and raw log

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
func (it *TappRegistryAppRegisteredIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(TappRegistryAppRegistered)
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
		it.Event = new(TappRegistryAppRegistered)
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
func (it *TappRegistryAppRegisteredIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *TappRegistryAppRegisteredIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// TappRegistryAppRegistered represents a AppRegistered event raised by the TappRegistry contract.
type TappRegistryAppRegistered struct {
	AppId       common.Hash
	Owner       common.Address
	ComposeHash []byte
	VolumesHash []byte
	ImageHashes [][]byte
	Raw         types.Log // Blockchain specific contextual infos
}

// FilterAppRegistered is a free log retrieval operation binding the contract event 0x73c2b93e6068a0defbdb453a135a44edae07f7a390cb98dde19a66dd453a3b8c.
//
// Solidity: event AppRegistered(string indexed appId, address indexed owner, bytes composeHash, bytes volumesHash, bytes[] imageHashes)
func (_TappRegistry *TappRegistryFilterer) FilterAppRegistered(opts *bind.FilterOpts, appId []string, owner []common.Address) (*TappRegistryAppRegisteredIterator, error) {

	var appIdRule []interface{}
	for _, appIdItem := range appId {
		appIdRule = append(appIdRule, appIdItem)
	}
	var ownerRule []interface{}
	for _, ownerItem := range owner {
		ownerRule = append(ownerRule, ownerItem)
	}

	logs, sub, err := _TappRegistry.contract.FilterLogs(opts, "AppRegistered", appIdRule, ownerRule)
	if err != nil {
		return nil, err
	}
	return &TappRegistryAppRegisteredIterator{contract: _TappRegistry.contract, event: "AppRegistered", logs: logs, sub: sub}, nil
}

// WatchAppRegistered is a free log subscription operation binding the contract event 0x73c2b93e6068a0defbdb453a135a44edae07f7a390cb98dde19a66dd453a3b8c.
//
// Solidity: event AppRegistered(string indexed appId, address indexed owner, bytes composeHash, bytes volumesHash, bytes[] imageHashes)
func (_TappRegistry *TappRegistryFilterer) WatchAppRegistered(opts *bind.WatchOpts, sink chan<- *TappRegistryAppRegistered, appId []string, owner []common.Address) (event.Subscription, error) {

	var appIdRule []interface{}
	for _, appIdItem := range appId {
		appIdRule = append(appIdRule, appIdItem)
	}
	var ownerRule []interface{}
	for _, ownerItem := range owner {
		ownerRule = append(ownerRule, ownerItem)
	}

	logs, sub, err := _TappRegistry.contract.WatchLogs(opts, "AppRegistered", appIdRule, ownerRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(TappRegistryAppRegistered)
				if err := _TappRegistry.contract.UnpackLog(event, "AppRegistered", log); err != nil {
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

// ParseAppRegistered is a log parse operation binding the contract event 0x73c2b93e6068a0defbdb453a135a44edae07f7a390cb98dde19a66dd453a3b8c.
//
// Solidity: event AppRegistered(string indexed appId, address indexed owner, bytes composeHash, bytes volumesHash, bytes[] imageHashes)
func (_TappRegistry *TappRegistryFilterer) ParseAppRegistered(log types.Log) (*TappRegistryAppRegistered, error) {
	event := new(TappRegistryAppRegistered)
	if err := _TappRegistry.contract.UnpackLog(event, "AppRegistered", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// TappRegistryAppUnregisteredIterator is returned from FilterAppUnregistered and is used to iterate over the raw logs and unpacked data for AppUnregistered events raised by the TappRegistry contract.
type TappRegistryAppUnregisteredIterator struct {
	Event *TappRegistryAppUnregistered // Event containing the contract specifics and raw log

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
func (it *TappRegistryAppUnregisteredIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(TappRegistryAppUnregistered)
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
		it.Event = new(TappRegistryAppUnregistered)
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
func (it *TappRegistryAppUnregisteredIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *TappRegistryAppUnregisteredIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// TappRegistryAppUnregistered represents a AppUnregistered event raised by the TappRegistry contract.
type TappRegistryAppUnregistered struct {
	AppId common.Hash
	Owner common.Address
	Raw   types.Log // Blockchain specific contextual infos
}

// FilterAppUnregistered is a free log retrieval operation binding the contract event 0x908e8e2c685576ca73868b684d556112c8f251c80c2da02725292a2fac00d079.
//
// Solidity: event AppUnregistered(string indexed appId, address indexed owner)
func (_TappRegistry *TappRegistryFilterer) FilterAppUnregistered(opts *bind.FilterOpts, appId []string, owner []common.Address) (*TappRegistryAppUnregisteredIterator, error) {

	var appIdRule []interface{}
	for _, appIdItem := range appId {
		appIdRule = append(appIdRule, appIdItem)
	}
	var ownerRule []interface{}
	for _, ownerItem := range owner {
		ownerRule = append(ownerRule, ownerItem)
	}

	logs, sub, err := _TappRegistry.contract.FilterLogs(opts, "AppUnregistered", appIdRule, ownerRule)
	if err != nil {
		return nil, err
	}
	return &TappRegistryAppUnregisteredIterator{contract: _TappRegistry.contract, event: "AppUnregistered", logs: logs, sub: sub}, nil
}

// WatchAppUnregistered is a free log subscription operation binding the contract event 0x908e8e2c685576ca73868b684d556112c8f251c80c2da02725292a2fac00d079.
//
// Solidity: event AppUnregistered(string indexed appId, address indexed owner)
func (_TappRegistry *TappRegistryFilterer) WatchAppUnregistered(opts *bind.WatchOpts, sink chan<- *TappRegistryAppUnregistered, appId []string, owner []common.Address) (event.Subscription, error) {

	var appIdRule []interface{}
	for _, appIdItem := range appId {
		appIdRule = append(appIdRule, appIdItem)
	}
	var ownerRule []interface{}
	for _, ownerItem := range owner {
		ownerRule = append(ownerRule, ownerItem)
	}

	logs, sub, err := _TappRegistry.contract.WatchLogs(opts, "AppUnregistered", appIdRule, ownerRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(TappRegistryAppUnregistered)
				if err := _TappRegistry.contract.UnpackLog(event, "AppUnregistered", log); err != nil {
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

// ParseAppUnregistered is a log parse operation binding the contract event 0x908e8e2c685576ca73868b684d556112c8f251c80c2da02725292a2fac00d079.
//
// Solidity: event AppUnregistered(string indexed appId, address indexed owner)
func (_TappRegistry *TappRegistryFilterer) ParseAppUnregistered(log types.Log) (*TappRegistryAppUnregistered, error) {
	event := new(TappRegistryAppUnregistered)
	if err := _TappRegistry.contract.UnpackLog(event, "AppUnregistered", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// TappRegistryAppUpdatedIterator is returned from FilterAppUpdated and is used to iterate over the raw logs and unpacked data for AppUpdated events raised by the TappRegistry contract.
type TappRegistryAppUpdatedIterator struct {
	Event *TappRegistryAppUpdated // Event containing the contract specifics and raw log

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
func (it *TappRegistryAppUpdatedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(TappRegistryAppUpdated)
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
		it.Event = new(TappRegistryAppUpdated)
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
func (it *TappRegistryAppUpdatedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *TappRegistryAppUpdatedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// TappRegistryAppUpdated represents a AppUpdated event raised by the TappRegistry contract.
type TappRegistryAppUpdated struct {
	AppId         common.Hash
	NewAckVersion *big.Int
	ComposeHash   []byte
	VolumesHash   []byte
	ImageHashes   [][]byte
	Raw           types.Log // Blockchain specific contextual infos
}

// FilterAppUpdated is a free log retrieval operation binding the contract event 0xd5ad7ec0e787a4a6ba8a2f60cff8dd2d433777583ad661e282d37efb7208bf1a.
//
// Solidity: event AppUpdated(string indexed appId, uint256 newAckVersion, bytes composeHash, bytes volumesHash, bytes[] imageHashes)
func (_TappRegistry *TappRegistryFilterer) FilterAppUpdated(opts *bind.FilterOpts, appId []string) (*TappRegistryAppUpdatedIterator, error) {

	var appIdRule []interface{}
	for _, appIdItem := range appId {
		appIdRule = append(appIdRule, appIdItem)
	}

	logs, sub, err := _TappRegistry.contract.FilterLogs(opts, "AppUpdated", appIdRule)
	if err != nil {
		return nil, err
	}
	return &TappRegistryAppUpdatedIterator{contract: _TappRegistry.contract, event: "AppUpdated", logs: logs, sub: sub}, nil
}

// WatchAppUpdated is a free log subscription operation binding the contract event 0xd5ad7ec0e787a4a6ba8a2f60cff8dd2d433777583ad661e282d37efb7208bf1a.
//
// Solidity: event AppUpdated(string indexed appId, uint256 newAckVersion, bytes composeHash, bytes volumesHash, bytes[] imageHashes)
func (_TappRegistry *TappRegistryFilterer) WatchAppUpdated(opts *bind.WatchOpts, sink chan<- *TappRegistryAppUpdated, appId []string) (event.Subscription, error) {

	var appIdRule []interface{}
	for _, appIdItem := range appId {
		appIdRule = append(appIdRule, appIdItem)
	}

	logs, sub, err := _TappRegistry.contract.WatchLogs(opts, "AppUpdated", appIdRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(TappRegistryAppUpdated)
				if err := _TappRegistry.contract.UnpackLog(event, "AppUpdated", log); err != nil {
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

// ParseAppUpdated is a log parse operation binding the contract event 0xd5ad7ec0e787a4a6ba8a2f60cff8dd2d433777583ad661e282d37efb7208bf1a.
//
// Solidity: event AppUpdated(string indexed appId, uint256 newAckVersion, bytes composeHash, bytes volumesHash, bytes[] imageHashes)
func (_TappRegistry *TappRegistryFilterer) ParseAppUpdated(log types.Log) (*TappRegistryAppUpdated, error) {
	event := new(TappRegistryAppUpdated)
	if err := _TappRegistry.contract.UnpackLog(event, "AppUpdated", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// TappRegistryInvalidatorAuthorizedIterator is returned from FilterInvalidatorAuthorized and is used to iterate over the raw logs and unpacked data for InvalidatorAuthorized events raised by the TappRegistry contract.
type TappRegistryInvalidatorAuthorizedIterator struct {
	Event *TappRegistryInvalidatorAuthorized // Event containing the contract specifics and raw log

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
func (it *TappRegistryInvalidatorAuthorizedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(TappRegistryInvalidatorAuthorized)
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
		it.Event = new(TappRegistryInvalidatorAuthorized)
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
func (it *TappRegistryInvalidatorAuthorizedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *TappRegistryInvalidatorAuthorizedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// TappRegistryInvalidatorAuthorized represents a InvalidatorAuthorized event raised by the TappRegistry contract.
type TappRegistryInvalidatorAuthorized struct {
	AppId       common.Hash
	Invalidator common.Address
	Raw         types.Log // Blockchain specific contextual infos
}

// FilterInvalidatorAuthorized is a free log retrieval operation binding the contract event 0x52a10e35e2583245a7f65cf055ea7d916b2d9925d70584a3f18647c2f8a217ed.
//
// Solidity: event InvalidatorAuthorized(string indexed appId, address indexed invalidator)
func (_TappRegistry *TappRegistryFilterer) FilterInvalidatorAuthorized(opts *bind.FilterOpts, appId []string, invalidator []common.Address) (*TappRegistryInvalidatorAuthorizedIterator, error) {

	var appIdRule []interface{}
	for _, appIdItem := range appId {
		appIdRule = append(appIdRule, appIdItem)
	}
	var invalidatorRule []interface{}
	for _, invalidatorItem := range invalidator {
		invalidatorRule = append(invalidatorRule, invalidatorItem)
	}

	logs, sub, err := _TappRegistry.contract.FilterLogs(opts, "InvalidatorAuthorized", appIdRule, invalidatorRule)
	if err != nil {
		return nil, err
	}
	return &TappRegistryInvalidatorAuthorizedIterator{contract: _TappRegistry.contract, event: "InvalidatorAuthorized", logs: logs, sub: sub}, nil
}

// WatchInvalidatorAuthorized is a free log subscription operation binding the contract event 0x52a10e35e2583245a7f65cf055ea7d916b2d9925d70584a3f18647c2f8a217ed.
//
// Solidity: event InvalidatorAuthorized(string indexed appId, address indexed invalidator)
func (_TappRegistry *TappRegistryFilterer) WatchInvalidatorAuthorized(opts *bind.WatchOpts, sink chan<- *TappRegistryInvalidatorAuthorized, appId []string, invalidator []common.Address) (event.Subscription, error) {

	var appIdRule []interface{}
	for _, appIdItem := range appId {
		appIdRule = append(appIdRule, appIdItem)
	}
	var invalidatorRule []interface{}
	for _, invalidatorItem := range invalidator {
		invalidatorRule = append(invalidatorRule, invalidatorItem)
	}

	logs, sub, err := _TappRegistry.contract.WatchLogs(opts, "InvalidatorAuthorized", appIdRule, invalidatorRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(TappRegistryInvalidatorAuthorized)
				if err := _TappRegistry.contract.UnpackLog(event, "InvalidatorAuthorized", log); err != nil {
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

// ParseInvalidatorAuthorized is a log parse operation binding the contract event 0x52a10e35e2583245a7f65cf055ea7d916b2d9925d70584a3f18647c2f8a217ed.
//
// Solidity: event InvalidatorAuthorized(string indexed appId, address indexed invalidator)
func (_TappRegistry *TappRegistryFilterer) ParseInvalidatorAuthorized(log types.Log) (*TappRegistryInvalidatorAuthorized, error) {
	event := new(TappRegistryInvalidatorAuthorized)
	if err := _TappRegistry.contract.UnpackLog(event, "InvalidatorAuthorized", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// TappRegistryInvalidatorRevokedIterator is returned from FilterInvalidatorRevoked and is used to iterate over the raw logs and unpacked data for InvalidatorRevoked events raised by the TappRegistry contract.
type TappRegistryInvalidatorRevokedIterator struct {
	Event *TappRegistryInvalidatorRevoked // Event containing the contract specifics and raw log

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
func (it *TappRegistryInvalidatorRevokedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(TappRegistryInvalidatorRevoked)
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
		it.Event = new(TappRegistryInvalidatorRevoked)
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
func (it *TappRegistryInvalidatorRevokedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *TappRegistryInvalidatorRevokedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// TappRegistryInvalidatorRevoked represents a InvalidatorRevoked event raised by the TappRegistry contract.
type TappRegistryInvalidatorRevoked struct {
	AppId       common.Hash
	Invalidator common.Address
	Raw         types.Log // Blockchain specific contextual infos
}

// FilterInvalidatorRevoked is a free log retrieval operation binding the contract event 0x2863a030e8c1f438acd5dca0a1df3257a0d90658b0bfcb8fddd3d958495e8f45.
//
// Solidity: event InvalidatorRevoked(string indexed appId, address indexed invalidator)
func (_TappRegistry *TappRegistryFilterer) FilterInvalidatorRevoked(opts *bind.FilterOpts, appId []string, invalidator []common.Address) (*TappRegistryInvalidatorRevokedIterator, error) {

	var appIdRule []interface{}
	for _, appIdItem := range appId {
		appIdRule = append(appIdRule, appIdItem)
	}
	var invalidatorRule []interface{}
	for _, invalidatorItem := range invalidator {
		invalidatorRule = append(invalidatorRule, invalidatorItem)
	}

	logs, sub, err := _TappRegistry.contract.FilterLogs(opts, "InvalidatorRevoked", appIdRule, invalidatorRule)
	if err != nil {
		return nil, err
	}
	return &TappRegistryInvalidatorRevokedIterator{contract: _TappRegistry.contract, event: "InvalidatorRevoked", logs: logs, sub: sub}, nil
}

// WatchInvalidatorRevoked is a free log subscription operation binding the contract event 0x2863a030e8c1f438acd5dca0a1df3257a0d90658b0bfcb8fddd3d958495e8f45.
//
// Solidity: event InvalidatorRevoked(string indexed appId, address indexed invalidator)
func (_TappRegistry *TappRegistryFilterer) WatchInvalidatorRevoked(opts *bind.WatchOpts, sink chan<- *TappRegistryInvalidatorRevoked, appId []string, invalidator []common.Address) (event.Subscription, error) {

	var appIdRule []interface{}
	for _, appIdItem := range appId {
		appIdRule = append(appIdRule, appIdItem)
	}
	var invalidatorRule []interface{}
	for _, invalidatorItem := range invalidator {
		invalidatorRule = append(invalidatorRule, invalidatorItem)
	}

	logs, sub, err := _TappRegistry.contract.WatchLogs(opts, "InvalidatorRevoked", appIdRule, invalidatorRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(TappRegistryInvalidatorRevoked)
				if err := _TappRegistry.contract.UnpackLog(event, "InvalidatorRevoked", log); err != nil {
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

// ParseInvalidatorRevoked is a log parse operation binding the contract event 0x2863a030e8c1f438acd5dca0a1df3257a0d90658b0bfcb8fddd3d958495e8f45.
//
// Solidity: event InvalidatorRevoked(string indexed appId, address indexed invalidator)
func (_TappRegistry *TappRegistryFilterer) ParseInvalidatorRevoked(log types.Log) (*TappRegistryInvalidatorRevoked, error) {
	event := new(TappRegistryInvalidatorRevoked)
	if err := _TappRegistry.contract.UnpackLog(event, "InvalidatorRevoked", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// TappRegistryLockPeriodUpdatedIterator is returned from FilterLockPeriodUpdated and is used to iterate over the raw logs and unpacked data for LockPeriodUpdated events raised by the TappRegistry contract.
type TappRegistryLockPeriodUpdatedIterator struct {
	Event *TappRegistryLockPeriodUpdated // Event containing the contract specifics and raw log

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
func (it *TappRegistryLockPeriodUpdatedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(TappRegistryLockPeriodUpdated)
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
		it.Event = new(TappRegistryLockPeriodUpdated)
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
func (it *TappRegistryLockPeriodUpdatedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *TappRegistryLockPeriodUpdatedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// TappRegistryLockPeriodUpdated represents a LockPeriodUpdated event raised by the TappRegistry contract.
type TappRegistryLockPeriodUpdated struct {
	OldPeriod *big.Int
	NewPeriod *big.Int
	Raw       types.Log // Blockchain specific contextual infos
}

// FilterLockPeriodUpdated is a free log retrieval operation binding the contract event 0x5dd7b309b7bc5010d9c96159ee535a428121d6803cb847792402fffccaf1569a.
//
// Solidity: event LockPeriodUpdated(uint256 oldPeriod, uint256 newPeriod)
func (_TappRegistry *TappRegistryFilterer) FilterLockPeriodUpdated(opts *bind.FilterOpts) (*TappRegistryLockPeriodUpdatedIterator, error) {

	logs, sub, err := _TappRegistry.contract.FilterLogs(opts, "LockPeriodUpdated")
	if err != nil {
		return nil, err
	}
	return &TappRegistryLockPeriodUpdatedIterator{contract: _TappRegistry.contract, event: "LockPeriodUpdated", logs: logs, sub: sub}, nil
}

// WatchLockPeriodUpdated is a free log subscription operation binding the contract event 0x5dd7b309b7bc5010d9c96159ee535a428121d6803cb847792402fffccaf1569a.
//
// Solidity: event LockPeriodUpdated(uint256 oldPeriod, uint256 newPeriod)
func (_TappRegistry *TappRegistryFilterer) WatchLockPeriodUpdated(opts *bind.WatchOpts, sink chan<- *TappRegistryLockPeriodUpdated) (event.Subscription, error) {

	logs, sub, err := _TappRegistry.contract.WatchLogs(opts, "LockPeriodUpdated")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(TappRegistryLockPeriodUpdated)
				if err := _TappRegistry.contract.UnpackLog(event, "LockPeriodUpdated", log); err != nil {
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

// ParseLockPeriodUpdated is a log parse operation binding the contract event 0x5dd7b309b7bc5010d9c96159ee535a428121d6803cb847792402fffccaf1569a.
//
// Solidity: event LockPeriodUpdated(uint256 oldPeriod, uint256 newPeriod)
func (_TappRegistry *TappRegistryFilterer) ParseLockPeriodUpdated(log types.Log) (*TappRegistryLockPeriodUpdated, error) {
	event := new(TappRegistryLockPeriodUpdated)
	if err := _TappRegistry.contract.UnpackLog(event, "LockPeriodUpdated", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// TappRegistryMinStakeUpdatedIterator is returned from FilterMinStakeUpdated and is used to iterate over the raw logs and unpacked data for MinStakeUpdated events raised by the TappRegistry contract.
type TappRegistryMinStakeUpdatedIterator struct {
	Event *TappRegistryMinStakeUpdated // Event containing the contract specifics and raw log

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
func (it *TappRegistryMinStakeUpdatedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(TappRegistryMinStakeUpdated)
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
		it.Event = new(TappRegistryMinStakeUpdated)
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
func (it *TappRegistryMinStakeUpdatedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *TappRegistryMinStakeUpdatedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// TappRegistryMinStakeUpdated represents a MinStakeUpdated event raised by the TappRegistry contract.
type TappRegistryMinStakeUpdated struct {
	OldAmount *big.Int
	NewAmount *big.Int
	Raw       types.Log // Blockchain specific contextual infos
}

// FilterMinStakeUpdated is a free log retrieval operation binding the contract event 0x171aabb8815c02fd00303450a77058600e3661eb75ce2e77972c0f080bc7099d.
//
// Solidity: event MinStakeUpdated(uint256 oldAmount, uint256 newAmount)
func (_TappRegistry *TappRegistryFilterer) FilterMinStakeUpdated(opts *bind.FilterOpts) (*TappRegistryMinStakeUpdatedIterator, error) {

	logs, sub, err := _TappRegistry.contract.FilterLogs(opts, "MinStakeUpdated")
	if err != nil {
		return nil, err
	}
	return &TappRegistryMinStakeUpdatedIterator{contract: _TappRegistry.contract, event: "MinStakeUpdated", logs: logs, sub: sub}, nil
}

// WatchMinStakeUpdated is a free log subscription operation binding the contract event 0x171aabb8815c02fd00303450a77058600e3661eb75ce2e77972c0f080bc7099d.
//
// Solidity: event MinStakeUpdated(uint256 oldAmount, uint256 newAmount)
func (_TappRegistry *TappRegistryFilterer) WatchMinStakeUpdated(opts *bind.WatchOpts, sink chan<- *TappRegistryMinStakeUpdated) (event.Subscription, error) {

	logs, sub, err := _TappRegistry.contract.WatchLogs(opts, "MinStakeUpdated")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(TappRegistryMinStakeUpdated)
				if err := _TappRegistry.contract.UnpackLog(event, "MinStakeUpdated", log); err != nil {
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

// ParseMinStakeUpdated is a log parse operation binding the contract event 0x171aabb8815c02fd00303450a77058600e3661eb75ce2e77972c0f080bc7099d.
//
// Solidity: event MinStakeUpdated(uint256 oldAmount, uint256 newAmount)
func (_TappRegistry *TappRegistryFilterer) ParseMinStakeUpdated(log types.Log) (*TappRegistryMinStakeUpdated, error) {
	event := new(TappRegistryMinStakeUpdated)
	if err := _TappRegistry.contract.UnpackLog(event, "MinStakeUpdated", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// TappRegistryNodeUpdatedIterator is returned from FilterNodeUpdated and is used to iterate over the raw logs and unpacked data for NodeUpdated events raised by the TappRegistry contract.
type TappRegistryNodeUpdatedIterator struct {
	Event *TappRegistryNodeUpdated // Event containing the contract specifics and raw log

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
func (it *TappRegistryNodeUpdatedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(TappRegistryNodeUpdated)
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
		it.Event = new(TappRegistryNodeUpdated)
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
func (it *TappRegistryNodeUpdatedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *TappRegistryNodeUpdatedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// TappRegistryNodeUpdated represents a NodeUpdated event raised by the TappRegistry contract.
type TappRegistryNodeUpdated struct {
	AppId         common.Hash
	OldSigner     common.Address
	NewSigner     common.Address
	StakeAmount   *big.Int
	UnlockAt      *big.Int
	NewAckVersion *big.Int
	Raw           types.Log // Blockchain specific contextual infos
}

// FilterNodeUpdated is a free log retrieval operation binding the contract event 0xd73e35820e265b126d3b52d593770b4e054d26ac6eba69c7551e6595de8d4e2b.
//
// Solidity: event NodeUpdated(string indexed appId, address indexed oldSigner, address indexed newSigner, uint256 stakeAmount, uint256 unlockAt, uint256 newAckVersion)
func (_TappRegistry *TappRegistryFilterer) FilterNodeUpdated(opts *bind.FilterOpts, appId []string, oldSigner []common.Address, newSigner []common.Address) (*TappRegistryNodeUpdatedIterator, error) {

	var appIdRule []interface{}
	for _, appIdItem := range appId {
		appIdRule = append(appIdRule, appIdItem)
	}
	var oldSignerRule []interface{}
	for _, oldSignerItem := range oldSigner {
		oldSignerRule = append(oldSignerRule, oldSignerItem)
	}
	var newSignerRule []interface{}
	for _, newSignerItem := range newSigner {
		newSignerRule = append(newSignerRule, newSignerItem)
	}

	logs, sub, err := _TappRegistry.contract.FilterLogs(opts, "NodeUpdated", appIdRule, oldSignerRule, newSignerRule)
	if err != nil {
		return nil, err
	}
	return &TappRegistryNodeUpdatedIterator{contract: _TappRegistry.contract, event: "NodeUpdated", logs: logs, sub: sub}, nil
}

// WatchNodeUpdated is a free log subscription operation binding the contract event 0xd73e35820e265b126d3b52d593770b4e054d26ac6eba69c7551e6595de8d4e2b.
//
// Solidity: event NodeUpdated(string indexed appId, address indexed oldSigner, address indexed newSigner, uint256 stakeAmount, uint256 unlockAt, uint256 newAckVersion)
func (_TappRegistry *TappRegistryFilterer) WatchNodeUpdated(opts *bind.WatchOpts, sink chan<- *TappRegistryNodeUpdated, appId []string, oldSigner []common.Address, newSigner []common.Address) (event.Subscription, error) {

	var appIdRule []interface{}
	for _, appIdItem := range appId {
		appIdRule = append(appIdRule, appIdItem)
	}
	var oldSignerRule []interface{}
	for _, oldSignerItem := range oldSigner {
		oldSignerRule = append(oldSignerRule, oldSignerItem)
	}
	var newSignerRule []interface{}
	for _, newSignerItem := range newSigner {
		newSignerRule = append(newSignerRule, newSignerItem)
	}

	logs, sub, err := _TappRegistry.contract.WatchLogs(opts, "NodeUpdated", appIdRule, oldSignerRule, newSignerRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(TappRegistryNodeUpdated)
				if err := _TappRegistry.contract.UnpackLog(event, "NodeUpdated", log); err != nil {
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

// ParseNodeUpdated is a log parse operation binding the contract event 0xd73e35820e265b126d3b52d593770b4e054d26ac6eba69c7551e6595de8d4e2b.
//
// Solidity: event NodeUpdated(string indexed appId, address indexed oldSigner, address indexed newSigner, uint256 stakeAmount, uint256 unlockAt, uint256 newAckVersion)
func (_TappRegistry *TappRegistryFilterer) ParseNodeUpdated(log types.Log) (*TappRegistryNodeUpdated, error) {
	event := new(TappRegistryNodeUpdated)
	if err := _TappRegistry.contract.UnpackLog(event, "NodeUpdated", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// TappRegistryStakeWithdrawnIterator is returned from FilterStakeWithdrawn and is used to iterate over the raw logs and unpacked data for StakeWithdrawn events raised by the TappRegistry contract.
type TappRegistryStakeWithdrawnIterator struct {
	Event *TappRegistryStakeWithdrawn // Event containing the contract specifics and raw log

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
func (it *TappRegistryStakeWithdrawnIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(TappRegistryStakeWithdrawn)
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
		it.Event = new(TappRegistryStakeWithdrawn)
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
func (it *TappRegistryStakeWithdrawnIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *TappRegistryStakeWithdrawnIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// TappRegistryStakeWithdrawn represents a StakeWithdrawn event raised by the TappRegistry contract.
type TappRegistryStakeWithdrawn struct {
	Owner  common.Address
	Amount *big.Int
	Raw    types.Log // Blockchain specific contextual infos
}

// FilterStakeWithdrawn is a free log retrieval operation binding the contract event 0x8108595eb6bad3acefa9da467d90cc2217686d5c5ac85460f8b7849c840645fc.
//
// Solidity: event StakeWithdrawn(address indexed owner, uint256 amount)
func (_TappRegistry *TappRegistryFilterer) FilterStakeWithdrawn(opts *bind.FilterOpts, owner []common.Address) (*TappRegistryStakeWithdrawnIterator, error) {

	var ownerRule []interface{}
	for _, ownerItem := range owner {
		ownerRule = append(ownerRule, ownerItem)
	}

	logs, sub, err := _TappRegistry.contract.FilterLogs(opts, "StakeWithdrawn", ownerRule)
	if err != nil {
		return nil, err
	}
	return &TappRegistryStakeWithdrawnIterator{contract: _TappRegistry.contract, event: "StakeWithdrawn", logs: logs, sub: sub}, nil
}

// WatchStakeWithdrawn is a free log subscription operation binding the contract event 0x8108595eb6bad3acefa9da467d90cc2217686d5c5ac85460f8b7849c840645fc.
//
// Solidity: event StakeWithdrawn(address indexed owner, uint256 amount)
func (_TappRegistry *TappRegistryFilterer) WatchStakeWithdrawn(opts *bind.WatchOpts, sink chan<- *TappRegistryStakeWithdrawn, owner []common.Address) (event.Subscription, error) {

	var ownerRule []interface{}
	for _, ownerItem := range owner {
		ownerRule = append(ownerRule, ownerItem)
	}

	logs, sub, err := _TappRegistry.contract.WatchLogs(opts, "StakeWithdrawn", ownerRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(TappRegistryStakeWithdrawn)
				if err := _TappRegistry.contract.UnpackLog(event, "StakeWithdrawn", log); err != nil {
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

// ParseStakeWithdrawn is a log parse operation binding the contract event 0x8108595eb6bad3acefa9da467d90cc2217686d5c5ac85460f8b7849c840645fc.
//
// Solidity: event StakeWithdrawn(address indexed owner, uint256 amount)
func (_TappRegistry *TappRegistryFilterer) ParseStakeWithdrawn(log types.Log) (*TappRegistryStakeWithdrawn, error) {
	event := new(TappRegistryStakeWithdrawn)
	if err := _TappRegistry.contract.UnpackLog(event, "StakeWithdrawn", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}
