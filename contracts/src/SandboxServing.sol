// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/// @title SandboxServing
/// @notice On-chain billing settlement for 0G Sandbox (TEE-based voucher model)
/// @dev Upgradeable via BeaconProxy + UpgradeableBeacon pattern (ERC-1967).
///      Storage layout is fixed; use __gap for future fields.
contract SandboxServing {

    // ─── Constants ────────────────────────────────────────────────────────────

    uint256 public constant LOCK_TIME = 2 hours;

    /// @dev EIP-712 type hash — field order must match the Go voucher.Sign() implementation
    bytes32 private constant VOUCHER_TYPEHASH = keccak256(
        "SandboxVoucher(address user,address provider,bytes32 usageHash,uint256 nonce,uint256 totalFee)"
    );

    // ─── Structs ──────────────────────────────────────────────────────────────

    struct Account {
        uint256 balance;
        uint256 pendingRefund;
        uint256 refundUnlockAt;
        mapping(address => uint256) lastNonce;       // provider → last settled nonce
        mapping(address => bool)    teeAcknowledged; // provider → legacy bool ack (signerVersion==0)
        mapping(address => uint256) teeAckVersion;   // provider → version user last acked
    }

    struct Service {
        string  url;
        address teeSignerAddress;
        uint256 computePricePerMin;
        uint256 createFee;
        uint256 signerVersion; // incremented on every signer/price/fee change
    }

    struct SandboxVoucher {
        address user;
        address provider;
        uint256 totalFee;
        bytes32 usageHash;
        uint256 nonce;
        bytes   signature;
    }

    enum SettlementStatus {
        SUCCESS,               // 0
        INSUFFICIENT_BALANCE,  // 1
        PROVIDER_MISMATCH,     // 2
        NOT_ACKNOWLEDGED,      // 3
        INVALID_NONCE,         // 4
        INVALID_SIGNATURE      // 5
    }

    // ─── State (storage layout — must not change between upgrades) ────────────
    //
    // slot 0 (packed): _locked | _initialized
    bool private _locked;
    bool private _initialized;

    // slots 1–5: mappings
    mapping(address => Account) private _accounts;
    mapping(address => Service) public  services;
    mapping(address => bool)    public  serviceExists;
    mapping(address => uint256) public  providerEarnings;
    mapping(address => uint256) public  providerStakes;

    // slot 6: was immutable, now regular storage (set in initialize)
    uint256 public providerStake;

    // slot 7: was immutable, now regular storage (set in initialize)
    bytes32 private _domainSeparator;

    // slots 8–57: reserved for future upgrades
    uint256[50] private __gap;

    // ─── Events ───────────────────────────────────────────────────────────────

    event Deposited(address indexed recipient, address indexed sender, uint256 amount);
    event RefundRequested(address indexed user, uint256 amount, uint256 unlockAt);
    event RefundWithdrawn(address indexed user, uint256 amount);
    event VoucherSettled(
        address indexed user,
        address indexed provider,
        uint256         totalFee,
        bytes32         usageHash,
        uint256         nonce,
        SettlementStatus status
    );
    event EarningsWithdrawn(address indexed provider, uint256 amount);
    event ServiceUpdated(
        address indexed provider,
        string  url,
        address teeSignerAddress,
        uint256 signerVersion
    );
    event TEESignerAcknowledged(address indexed user, address indexed provider, bool acknowledged);

    // ─── Modifiers ────────────────────────────────────────────────────────────

    modifier nonReentrant() {
        require(!_locked, "reentrant");
        _locked = true;
        _;
        _locked = false;
    }

    /// @dev Prevents re-initialization. The no-arg constructor sets _initialized=true
    ///      on the implementation contract so nobody can call initialize() on it directly.
    modifier initializer() {
        require(!_initialized, "already initialized");
        _initialized = true;
        _;
    }

    // ─── Constructor / Initializer ─────────────────────────────────────────────

    /// @dev Locks the implementation contract so initialize() cannot be called on it.
    ///      When the BeaconProxy calls initialize() via delegatecall, _initialized lives
    ///      in the PROXY's storage, not in the implementation's, so the proxy can
    ///      initialize exactly once.
    constructor() {
        _initialized = true;
    }

    /// @notice Initialize the proxy.  Called once via BeaconProxy constructor through delegatecall.
    /// @dev address(this) = BeaconProxy address when invoked via delegatecall — correct for EIP-712.
    function initialize(uint256 providerStake_) external initializer {
        providerStake = providerStake_;
        _domainSeparator = keccak256(abi.encode(
            keccak256("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"),
            keccak256(bytes("0G Sandbox Serving")),
            keccak256(bytes("1")),
            block.chainid,
            address(this)
        ));
    }

    // ─── Account: deposit / refund ─────────────────────────────────────────────

    /// @notice Deposit ETH into recipient's billing account. Supports third-party top-up.
    function deposit(address recipient) external payable {
        require(msg.value > 0, "zero deposit");
        _accounts[recipient].balance += msg.value;
        emit Deposited(recipient, msg.sender, msg.value);
    }

    /// @notice Request a refund. Enters a lockTime period before withdrawal is allowed.
    /// @dev Cancels any existing pending refund first (re-enters balance).
    function requestRefund(uint256 amount) external {
        Account storage acct = _accounts[msg.sender];
        require(amount > 0, "zero amount");
        // Re-absorb any previous pending refund
        acct.balance += acct.pendingRefund;
        require(acct.balance >= amount, "insufficient balance");
        acct.balance -= amount;
        acct.pendingRefund = amount;
        acct.refundUnlockAt = block.timestamp + LOCK_TIME;
        emit RefundRequested(msg.sender, amount, acct.refundUnlockAt);
    }

    /// @notice Withdraw a previously requested refund after the lock period.
    function withdrawRefund() external nonReentrant {
        Account storage acct = _accounts[msg.sender];
        require(acct.pendingRefund > 0, "no pending refund");
        require(block.timestamp >= acct.refundUnlockAt, "refund locked");
        uint256 amount = acct.pendingRefund;
        acct.pendingRefund = 0;
        (bool ok,) = msg.sender.call{value: amount}("");
        require(ok, "transfer failed");
        emit RefundWithdrawn(msg.sender, amount);
    }

    // ─── Settlement ───────────────────────────────────────────────────────────

    /// @notice Settle a batch of TEE-signed vouchers. Only the provider can submit their own vouchers.
    function settleFeesWithTEE(SandboxVoucher[] calldata vouchers)
        external
        nonReentrant
        returns (SettlementStatus[] memory statuses)
    {
        statuses = new SettlementStatus[](vouchers.length);
        for (uint256 i = 0; i < vouchers.length; i++) {
            statuses[i] = _settleOne(vouchers[i]);
        }
    }

    function _settleOne(SandboxVoucher calldata v) internal returns (SettlementStatus) {
        if (v.provider != msg.sender || !serviceExists[v.provider]) {
            return SettlementStatus.PROVIDER_MISMATCH;
        }

        Account storage acct = _accounts[v.user];

        if (!_isAcknowledged(acct, v.provider)) {
            return SettlementStatus.NOT_ACKNOWLEDGED;
        }

        if (v.nonce <= acct.lastNonce[v.provider]) {
            return SettlementStatus.INVALID_NONCE;
        }

        if (!_verifySignature(v)) {
            return SettlementStatus.INVALID_SIGNATURE;
        }

        // Commit nonce before any state changes (prevents replay even on partial failures)
        acct.lastNonce[v.provider] = v.nonce;

        if (acct.balance >= v.totalFee) {
            // Full payment
            acct.balance -= v.totalFee;
            providerEarnings[v.provider] += v.totalFee;
            // Restore LIFO invariant: pendingRefund ≤ balance.
            // The excess is simply cancelled — it is NOT transferred to the provider.
            if (acct.pendingRefund > acct.balance) {
                acct.pendingRefund = acct.balance;
            }
            emit VoucherSettled(v.user, v.provider, v.totalFee, v.usageHash, v.nonce, SettlementStatus.SUCCESS);
            return SettlementStatus.SUCCESS;
        } else {
            // Drain everything (balance + pendingRefund)
            uint256 paid = acct.balance + acct.pendingRefund;
            acct.balance = 0;
            acct.pendingRefund = 0;
            providerEarnings[v.provider] += paid;
            emit VoucherSettled(v.user, v.provider, v.totalFee, v.usageHash, v.nonce, SettlementStatus.INSUFFICIENT_BALANCE);
            return SettlementStatus.INSUFFICIENT_BALANCE;
        }
    }

    /// @notice View-only preview of settlement results (for Auto-Settler pre-check).
    function previewSettlementResults(SandboxVoucher[] calldata vouchers)
        external
        view
        returns (SettlementStatus[] memory statuses)
    {
        statuses = new SettlementStatus[](vouchers.length);
        for (uint256 i = 0; i < vouchers.length; i++) {
            statuses[i] = _previewOne(vouchers[i]);
        }
    }

    function _previewOne(SandboxVoucher calldata v) internal view returns (SettlementStatus) {
        if (v.provider != msg.sender || !serviceExists[v.provider]) {
            return SettlementStatus.PROVIDER_MISMATCH;
        }
        Account storage acct = _accounts[v.user];
        if (!_isAcknowledged(acct, v.provider)) {
            return SettlementStatus.NOT_ACKNOWLEDGED;
        }
        if (v.nonce <= acct.lastNonce[v.provider]) {
            return SettlementStatus.INVALID_NONCE;
        }
        if (!_verifySignature(v)) {
            return SettlementStatus.INVALID_SIGNATURE;
        }
        if (acct.balance >= v.totalFee) {
            return SettlementStatus.SUCCESS;
        }
        return SettlementStatus.INSUFFICIENT_BALANCE;
    }

    /// @dev Returns true if the user has acknowledged the current service version for provider.
    ///      Backward-compatible: if signerVersion==0 (never changed), uses the legacy bool.
    ///      Once signerVersion>0, only the versioned ack is accepted.
    function _isAcknowledged(Account storage acct, address provider) internal view returns (bool) {
        uint256 version = services[provider].signerVersion;
        if (version == 0) {
            return acct.teeAcknowledged[provider];
        }
        return acct.teeAckVersion[provider] == version;
    }

    function _verifySignature(SandboxVoucher calldata v) internal view returns (bool) {
        bytes32 structHash = keccak256(abi.encode(
            VOUCHER_TYPEHASH,
            v.user,
            v.provider,
            v.usageHash,
            v.nonce,
            v.totalFee
        ));
        bytes32 digest = keccak256(abi.encodePacked("\x19\x01", _domainSeparator, structHash));
        address recovered = _ecrecover(digest, v.signature);
        return recovered != address(0) && recovered == services[v.provider].teeSignerAddress;
    }

    function _ecrecover(bytes32 digest, bytes memory sig) internal pure returns (address) {
        if (sig.length != 65) return address(0);
        bytes32 r;
        bytes32 s;
        uint8   v;
        assembly {
            r := mload(add(sig, 32))
            s := mload(add(sig, 64))
            v := byte(0, mload(add(sig, 96)))
        }
        if (v < 27) v += 27;
        if (v != 27 && v != 28) return address(0);
        return ecrecover(digest, v, r, s);
    }

    // ─── Provider earnings ────────────────────────────────────────────────────

    function withdrawEarnings() external nonReentrant {
        uint256 amount = providerEarnings[msg.sender];
        require(amount > 0, "no earnings");
        providerEarnings[msg.sender] = 0;
        (bool ok,) = msg.sender.call{value: amount}("");
        require(ok, "transfer failed");
        emit EarningsWithdrawn(msg.sender, amount);
    }

    // ─── Provider Management ──────────────────────────────────────────────────

    /// @notice Register or update provider service.
    /// @dev First registration requires staking providerStake ETH.
    ///      Any change to teeSignerAddress, computePricePerMin, or createFee increments
    ///      signerVersion, requiring all users to re-acknowledge before vouchers settle.
    function addOrUpdateService(
        string  calldata url,
        address teeSignerAddress,
        uint256 computePricePerMin,
        uint256 createFee
    ) external payable {
        bool isNew = !serviceExists[msg.sender];
        if (isNew) {
            require(msg.value >= providerStake, "insufficient stake");
            providerStakes[msg.sender] = msg.value;
            serviceExists[msg.sender] = true;
        }

        Service storage svc = services[msg.sender];
        bool serviceChanged = !isNew && (
            svc.teeSignerAddress   != teeSignerAddress   ||
            svc.computePricePerMin != computePricePerMin ||
            svc.createFee          != createFee
        );

        svc.url                = url;
        svc.teeSignerAddress   = teeSignerAddress;
        svc.computePricePerMin = computePricePerMin;
        svc.createFee          = createFee;
        if (serviceChanged) {
            svc.signerVersion += 1;
        }

        emit ServiceUpdated(msg.sender, url, teeSignerAddress, svc.signerVersion);
    }

    /// @notice Acknowledge (or revoke) the current service version for a provider.
    /// @dev When signerVersion==0 (legacy), writes the bool for backward compatibility.
    ///      When signerVersion>0, writes the versioned ack so future price changes invalidate it.
    function acknowledgeTEESigner(address provider, bool acknowledged) external {
        require(serviceExists[provider], "provider not found");
        uint256 version = services[provider].signerVersion;
        if (acknowledged) {
            if (version == 0) {
                _accounts[msg.sender].teeAcknowledged[provider] = true;
            } else {
                _accounts[msg.sender].teeAckVersion[provider] = version;
            }
        } else {
            _accounts[msg.sender].teeAcknowledged[provider] = false;
            _accounts[msg.sender].teeAckVersion[provider] = 0;
        }
        emit TEESignerAcknowledged(msg.sender, provider, acknowledged);
    }

    // ─── View Functions ───────────────────────────────────────────────────────

    function getAccount(address user)
        external
        view
        returns (uint256 balance, uint256 pendingRefund, uint256 refundUnlockAt)
    {
        Account storage a = _accounts[user];
        return (a.balance, a.pendingRefund, a.refundUnlockAt);
    }

    function getLastNonce(address user, address provider) external view returns (uint256) {
        return _accounts[user].lastNonce[provider];
    }

    function getProviderEarnings(address provider) external view returns (uint256) {
        return providerEarnings[provider];
    }

    /// @notice Returns true if user has acknowledged the CURRENT service version for provider.
    ///         Backward-compatible: uses legacy bool when signerVersion==0.
    function isTEEAcknowledged(address user, address provider) external view returns (bool) {
        if (!serviceExists[provider]) return false;
        return _isAcknowledged(_accounts[user], provider);
    }

    function domainSeparator() external view returns (bytes32) {
        return _domainSeparator;
    }
}
