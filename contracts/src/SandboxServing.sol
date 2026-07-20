// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/// @notice Minimal interface to TappRegistry. SandboxServing delegates TEE
///         signer identity, per-node stake, and user acknowledgement state
///         to TappRegistry. See https://github.com/0gfoundation/0g-tapp.
interface ITappRegistry {
    struct AppInfo {
        bytes   composeHash;
        bytes   volumesHash;
        bytes[] imageHashes;
        address owner;
        uint256 registeredAt;
    }
    struct NodeInfo {
        string  teeUrl;
        uint256 addedAt;
        uint256 stakeAmount;
    }
    function getAppInfo(string calldata appId) external view returns (AppInfo memory);
    function getNode(string calldata appId, address signer) external view returns (NodeInfo memory);
    function isAcknowledged(address user, string calldata appId) external view returns (bool);
    function isAuthorizedInvalidator(string calldata appId, address invalidator) external view returns (bool);
    function invalidateAcks(string calldata appId) external;
}

/// @title SandboxServing
/// @notice On-chain billing settlement for 0G Sandbox (TEE-based voucher model).
/// @dev Upgradeable via BeaconProxy + UpgradeableBeacon pattern (ERC-1967).
///      Storage layout is fixed; use __gap for future fields.
///
///      Trust delegation
///      ----------------
///      TEE signer identity, per-node stake, and user acknowledgement live
///      in TappRegistry. SandboxServing owns only commercial state:
///      service URL, prices, balances, settlement.
///
///      Identity model (v2: provider IS the TEE signer)
///      ------------------------------------------------
///      Every "provider" address in this contract — the services key, the
///      voucher payee, the (user, provider) balance bucket, the earnings
///      ledger — is the TEE-derived signer address of one node (one machine)
///      registered for the appId in TappRegistry. There is no separate
///      provider wallet. Consequences:
///        * a voucher is valid only if it was signed BY its own payee
///          (recovered == v.provider) and that address is an active node;
///          a node cannot settle vouchers naming another node as payee
///        * the TEE key cannot leave the enclave and rotates when the
///          machine is rebuilt, so all management (register / remove /
///          withdraw earnings) is done by the appId's TappRegistry owner,
///          not by the provider address itself
///        * on rotation the old signer's service is removed by the owner
///          (removeService sweeps pending earnings to the owner) and users
///          move their remaining balance via the normal refund flow
///
///      The app owner registers a node's service in four steps:
///        1. tappRegistry.registerApp(appId, ...)           — stakes per node
///        2. tappRegistry.addNode(appId, signer, teeUrl)    — one per machine
///        3. tappRegistry.authorizeInvalidator(appId, this) — lets us bump
///                                                            ackVersion on
///                                                            price changes
///        4. sandboxServing.addOrUpdateService(signer, url, appId, prices)
///
///      Voucher verification reads from TappRegistry at settle time (no local
///      mirror of signer state — prevents the silent-drift incidents that
///      caused 348k unsigned vouchers to accumulate in May 2026).
contract SandboxServing {

    // ─── Constants ────────────────────────────────────────────────────────────

    uint256 public constant LOCK_TIME = 2 hours;

    /// @dev EIP-712 type hash — field order must match the Go voucher.Sign() implementation.
    bytes32 private constant VOUCHER_TYPEHASH = keccak256(
        "SandboxVoucher(address user,address provider,bytes32 usageHash,uint256 nonce,uint256 totalFee)"
    );

    // ─── Structs ──────────────────────────────────────────────────────────────

    struct Account {
        mapping(address => uint256) balances;        // provider → balance
        mapping(address => uint256) pendingRefunds;  // provider → pending refund
        mapping(address => uint256) refundUnlockAts; // provider → refund unlock time
        mapping(address => uint256) lastNonce;       // provider → last settled nonce
    }

    struct Service {
        string  url;
        string  appId;                  // bound to tappRegistry's appId; set-once
        uint256 pricePerCPUPerMin;
        uint256 pricePerMemGBPerMin;
        uint256 createFee;
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

    // ─── State ────────────────────────────────────────────────────────────────

    bool private _locked;
    bool private _initialized;

    mapping(address => Account) private _accounts;
    mapping(address => Service) public  services;
    mapping(address => bool)    public  serviceExists;
    mapping(address => uint256) public  providerEarnings;

    bytes32 private _domainSeparator;
    address public  owner;

    /// @notice TappRegistry that holds signer identity, ack state, and stake.
    ///         Set in initialize(); owner can repoint via setTappRegistry when
    ///         TappRegistry is itself redeployed.
    ITappRegistry public tappRegistry;

    // Reserved for future upgrades.
    uint256[50] private __gap;

    // ─── Events ───────────────────────────────────────────────────────────────

    event Deposited(address indexed recipient, address indexed provider, address indexed sender, uint256 amount);
    event RefundRequested(address indexed user, address indexed provider, uint256 amount, uint256 unlockAt);
    event RefundWithdrawn(address indexed user, address indexed provider, uint256 amount);
    event VoucherSettled(
        address indexed user,
        address indexed provider,
        uint256         totalFee,
        bytes32         usageHash,
        uint256         nonce,
        SettlementStatus status
    );
    event EarningsWithdrawn(address indexed provider, address indexed to, uint256 amount);
    event ServiceUpdated(address indexed provider, string appId, string url);
    event ServiceRemoved(address indexed provider, address indexed appOwner);
    event OwnershipTransferred(address indexed previousOwner, address indexed newOwner);
    event TappRegistryUpdated(address indexed previousRegistry, address indexed newRegistry);

    // ─── Modifiers ────────────────────────────────────────────────────────────

    modifier onlyOwner() {
        require(msg.sender == owner, "not owner");
        _;
    }

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

    // ─── Constructor / Initializer ────────────────────────────────────────────

    /// @dev Locks the implementation contract so initialize() cannot be called on it.
    ///      When the BeaconProxy calls initialize() via delegatecall, _initialized lives
    ///      in the PROXY's storage, not in the implementation's, so the proxy can
    ///      initialize exactly once.
    constructor() {
        _initialized = true;
    }

    /// @notice Initialize the proxy. Called once via BeaconProxy constructor through delegatecall.
    /// @dev address(this) = BeaconProxy address when invoked via delegatecall — correct for EIP-712.
    function initialize(address tappRegistry_) external initializer {
        require(tappRegistry_ != address(0), "zero tappRegistry");
        owner = msg.sender;
        tappRegistry = ITappRegistry(tappRegistry_);
        _domainSeparator = keccak256(abi.encode(
            keccak256("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"),
            keccak256(bytes("0G Sandbox Serving")),
            keccak256(bytes("1")),
            block.chainid,
            address(this)
        ));
    }

    // ─── Account: deposit / refund ────────────────────────────────────────────

    /// @notice Deposit ETH into recipient's billing account for a specific provider.
    function deposit(address recipient, address provider) external payable {
        require(msg.value > 0, "zero deposit");
        _accounts[recipient].balances[provider] += msg.value;
        emit Deposited(recipient, provider, msg.sender, msg.value);
    }

    /// @notice Request a refund from a specific provider's balance bucket.
    /// @dev Cancels any existing pending refund for this provider first (re-enters balance).
    function requestRefund(address provider, uint256 amount) external {
        Account storage acct = _accounts[msg.sender];
        require(amount > 0, "zero amount");
        // Re-absorb any previous pending refund for this provider
        acct.balances[provider] += acct.pendingRefunds[provider];
        require(acct.balances[provider] >= amount, "insufficient balance");
        acct.balances[provider] -= amount;
        acct.pendingRefunds[provider] = amount;
        acct.refundUnlockAts[provider] = block.timestamp + LOCK_TIME;
        emit RefundRequested(msg.sender, provider, amount, acct.refundUnlockAts[provider]);
    }

    /// @notice Withdraw a previously requested refund after the lock period.
    function withdrawRefund(address provider) external nonReentrant {
        Account storage acct = _accounts[msg.sender];
        require(acct.pendingRefunds[provider] > 0, "no pending refund");
        require(block.timestamp >= acct.refundUnlockAts[provider], "refund locked");
        uint256 amount = acct.pendingRefunds[provider];
        acct.pendingRefunds[provider] = 0;
        (bool ok,) = msg.sender.call{value: amount}("");
        require(ok, "transfer failed");
        emit RefundWithdrawn(msg.sender, provider, amount);
    }

    // ─── Settlement ───────────────────────────────────────────────────────────

    /// @notice Settle a batch of TEE-signed vouchers. Anyone can submit; provider is identified by v.provider.
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
        if (!serviceExists[v.provider]) {
            return SettlementStatus.PROVIDER_MISMATCH;
        }

        string memory appId = services[v.provider].appId;
        if (!tappRegistry.isAcknowledged(v.user, appId)) {
            return SettlementStatus.NOT_ACKNOWLEDGED;
        }

        Account storage acct = _accounts[v.user];
        if (v.nonce <= acct.lastNonce[v.provider]) {
            return SettlementStatus.INVALID_NONCE;
        }

        if (!_verifySignature(v, appId)) {
            return SettlementStatus.INVALID_SIGNATURE;
        }

        // Commit nonce before any state changes (prevents replay even on partial failures)
        acct.lastNonce[v.provider] = v.nonce;

        if (acct.balances[v.provider] >= v.totalFee) {
            acct.balances[v.provider] -= v.totalFee;
            providerEarnings[v.provider] += v.totalFee;
            // Restore LIFO invariant: pendingRefunds[provider] ≤ balances[provider].
            if (acct.pendingRefunds[v.provider] > acct.balances[v.provider]) {
                acct.pendingRefunds[v.provider] = acct.balances[v.provider];
            }
            emit VoucherSettled(v.user, v.provider, v.totalFee, v.usageHash, v.nonce, SettlementStatus.SUCCESS);
            return SettlementStatus.SUCCESS;
        } else {
            uint256 paid = acct.balances[v.provider] + acct.pendingRefunds[v.provider];
            acct.balances[v.provider] = 0;
            acct.pendingRefunds[v.provider] = 0;
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
        if (!serviceExists[v.provider]) {
            return SettlementStatus.PROVIDER_MISMATCH;
        }
        string memory appId = services[v.provider].appId;
        if (!tappRegistry.isAcknowledged(v.user, appId)) {
            return SettlementStatus.NOT_ACKNOWLEDGED;
        }
        Account storage acct = _accounts[v.user];
        if (v.nonce <= acct.lastNonce[v.provider]) {
            return SettlementStatus.INVALID_NONCE;
        }
        if (!_verifySignature(v, appId)) {
            return SettlementStatus.INVALID_SIGNATURE;
        }
        if (acct.balances[v.provider] >= v.totalFee) {
            return SettlementStatus.SUCCESS;
        }
        return SettlementStatus.INSUFFICIENT_BALANCE;
    }

    /// @dev ECDSA-recovers the voucher signer locally. Valid iff the voucher
    ///      was signed by its own payee (provider IS the TEE signer) and that
    ///      address is an active node of the app in TappRegistry. A node can
    ///      therefore never settle vouchers naming another node as payee.
    function _verifySignature(SandboxVoucher calldata v, string memory appId) internal view returns (bool) {
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
        if (recovered == address(0) || recovered != v.provider) return false;
        return tappRegistry.getNode(appId, recovered).addedAt != 0;
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

    /// @notice Withdraw a node's accrued earnings to the app owner. The
    ///         provider address is a TEE signer whose key never leaves the
    ///         enclave (and dies with the machine), so payout authority
    ///         belongs to the appId's TappRegistry owner.
    function withdrawEarnings(address signer) external nonReentrant {
        require(_isAppOwnerOf(signer, msg.sender), "not app owner");
        uint256 amount = providerEarnings[signer];
        require(amount > 0, "no earnings");
        providerEarnings[signer] = 0;
        (bool ok,) = msg.sender.call{value: amount}("");
        require(ok, "transfer failed");
        emit EarningsWithdrawn(signer, msg.sender, amount);
    }

    /// @dev True iff `caller` is the TappRegistry owner of the appId that
    ///      `signer`'s service is bound to. Requires an existing service entry.
    function _isAppOwnerOf(address signer, address caller) internal view returns (bool) {
        string memory appId = services[signer].appId;
        if (bytes(appId).length == 0) return false;
        return tappRegistry.getAppInfo(appId).owner == caller;
    }

    // ─── Admin ────────────────────────────────────────────────────────────────

    /// @notice Transfer contract ownership to a new address.
    function transferOwnership(address newOwner) external onlyOwner {
        require(newOwner != address(0), "zero address");
        emit OwnershipTransferred(owner, newOwner);
        owner = newOwner;
    }

    /// @notice Repoint TappRegistry. Use when TappRegistry itself is redeployed.
    ///         Existing services keep their appId binding; ack state is read
    ///         from the new registry from this point on.
    function setTappRegistry(address newRegistry) external onlyOwner {
        require(newRegistry != address(0), "zero tappRegistry");
        emit TappRegistryUpdated(address(tappRegistry), newRegistry);
        tappRegistry = ITappRegistry(newRegistry);
    }

    // ─── Provider Management ──────────────────────────────────────────────────

    /// @notice Register or update a node's service. Called by the appId's
    ///         TappRegistry owner; `signer` is the node's TEE signer address
    ///         and must already be an active node of the appId. The contract
    ///         must also be authorized as an invalidator for the appId
    ///         (see ITappRegistry.authorizeInvalidator).
    /// @dev appId is set-once per signer; later updates must pass the same
    ///      appId or revert. Stake is collected by TappRegistry (per node);
    ///      not collected here.
    ///
    ///      Price/createFee changes call tappRegistry.invalidateAcks(appId),
    ///      bumping the app's ackVersion so existing user acks become stale.
    ///      URL-only changes do NOT invalidate (URL drift can't redirect
    ///      vouchers — signature/ack still root at the on-chain trust state).
    function addOrUpdateService(
        address          signer,
        string  calldata url,
        string  calldata appId,
        uint256 pricePerCPUPerMin,
        uint256 createFee,
        uint256 pricePerMemGBPerMin
    ) external {
        require(tappRegistry.getAppInfo(appId).owner == msg.sender, "not app owner");
        require(tappRegistry.getNode(appId, signer).addedAt != 0, "signer not an active node");
        require(
            tappRegistry.isAuthorizedInvalidator(appId, address(this)),
            "sandbox not authorized as invalidator"
        );

        Service storage svc = services[signer];
        bool isNew = bytes(svc.appId).length == 0;
        if (isNew) {
            svc.appId = appId;
        } else {
            require(
                keccak256(bytes(svc.appId)) == keccak256(bytes(appId)),
                "appId immutable; remove to change"
            );
        }

        bool pricesChanged = !isNew && (
            svc.pricePerCPUPerMin   != pricePerCPUPerMin   ||
            svc.pricePerMemGBPerMin != pricePerMemGBPerMin ||
            svc.createFee           != createFee
        );

        svc.url                 = url;
        svc.pricePerCPUPerMin   = pricePerCPUPerMin;
        svc.createFee           = createFee;
        svc.pricePerMemGBPerMin = pricePerMemGBPerMin;
        serviceExists[signer] = true;

        if (pricesChanged) {
            tappRegistry.invalidateAcks(appId);
        }

        emit ServiceUpdated(signer, appId, url);
    }

    /// @notice Remove a node's service registration. Called by the appId's
    ///         TappRegistry owner — the signer key itself may be gone (it dies
    ///         with the machine), so removal cannot depend on it. Sweeps any
    ///         pending earnings to the owner in the same call: once the entry
    ///         is gone there is no appId left to authorize a later withdrawal
    ///         (and no new earnings can accrue — settlement requires
    ///         serviceExists).
    /// @dev Soft clear otherwise: user balances, pending refunds, and settled
    ///      nonces are keyed elsewhere and preserved — balances remain
    ///      refundable, and nonces stay put so old vouchers can't be replayed
    ///      after a re-register.
    function removeService(address signer) external nonReentrant {
        require(serviceExists[signer], "no service to remove");
        require(_isAppOwnerOf(signer, msg.sender), "not app owner");

        uint256 amount = providerEarnings[signer];
        providerEarnings[signer] = 0;
        delete services[signer];
        serviceExists[signer] = false;

        if (amount > 0) {
            (bool ok,) = msg.sender.call{value: amount}("");
            require(ok, "transfer failed");
            emit EarningsWithdrawn(signer, msg.sender, amount);
        }
        emit ServiceRemoved(signer, msg.sender);
    }

    // ─── View Functions ───────────────────────────────────────────────────────

    function getBalance(address user, address provider)
        external
        view
        returns (uint256 balance, uint256 pendingRefund, uint256 refundUnlockAt)
    {
        Account storage a = _accounts[user];
        return (a.balances[provider], a.pendingRefunds[provider], a.refundUnlockAts[provider]);
    }

    function balanceOfBatch(address[] calldata users, address provider)
        external
        view
        returns (uint256[] memory balances)
    {
        balances = new uint256[](users.length);
        for (uint256 i = 0; i < users.length; i++) {
            balances[i] = _accounts[users[i]].balances[provider];
        }
    }

    function getLastNonce(address user, address provider) external view returns (uint256) {
        return _accounts[user].lastNonce[provider];
    }

    function getProviderEarnings(address provider) external view returns (uint256) {
        return providerEarnings[provider];
    }

    /// @notice Backward-compat shim. New callers should query tappRegistry
    ///         directly: `tappRegistry.isAcknowledged(user, services(provider).appId)`.
    function isTEEAcknowledged(address user, address provider) external view returns (bool) {
        string memory appId = services[provider].appId;
        if (bytes(appId).length == 0) return false;
        return tappRegistry.isAcknowledged(user, appId);
    }

    function domainSeparator() external view returns (bytes32) {
        return _domainSeparator;
    }
}
