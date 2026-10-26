// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Test, console} from "forge-std/Test.sol";
import {SandboxServing, ITappRegistry} from "./SandboxServing.sol";
import {UpgradeableBeacon} from "./proxy/UpgradeableBeacon.sol";
import {BeaconProxy} from "./proxy/BeaconProxy.sol";

/// @dev Minimal in-memory TappRegistry stand-in for unit tests. Real
///      TappRegistry lives in the 0g-tapp repo; we don't pull it in as a
///      dependency for these tests — the interface surface SandboxServing
///      touches is small (5 functions), so a hand-written mock is simpler
///      and faster than wiring a multi-repo Solidity build.
contract MockTappRegistry is ITappRegistry {
    mapping(string => address) public appOwner;
    mapping(string => mapping(address => uint256)) public nodeAddedAt;   // appId => signer => addedAt
    mapping(address => mapping(string => bool)) public userAcked;        // user => appId => acked
    mapping(string => mapping(address => bool)) public invalidators;     // appId => contract => authorized
    mapping(string => uint256) public appRegisteredAt;                   // appId => registeredAt
    mapping(string => bytes[]) internal appImageHashes;                  // appId => imageHashes
    uint256 public invalidateCount;

    function setAppOwner(string calldata appId, address ownerAddr) external {
        appOwner[appId] = ownerAddr;
        if (appRegisteredAt[appId] == 0) appRegisteredAt[appId] = block.timestamp;
    }
    /// The app's last node removed and the appId registered again — by
    /// `newOwner`, as a new TappRegistry instance (new registeredAt).
    function reRegister(string calldata appId, address newOwner) external {
        appOwner[appId] = newOwner;
        appRegisteredAt[appId] += 1;
    }
    function setImageHashes(string calldata appId, uint256 n) external {
        delete appImageHashes[appId];
        for (uint256 i = 0; i < n; i++) {
            appImageHashes[appId].push(abi.encodePacked(keccak256(abi.encode(appId, i))));
        }
    }
    function addNode(string calldata appId, address signer) external {
        nodeAddedAt[appId][signer] = block.timestamp;
    }
    function removeNode(string calldata appId, address signer) external {
        nodeAddedAt[appId][signer] = 0;
    }
    function setAck(address user, string calldata appId, bool v) external {
        userAcked[user][appId] = v;
    }
    function authorize(string calldata appId, address inv) external {
        invalidators[appId][inv] = true;
    }

    // ── ITappRegistry impl ──────────────────────────────────────────────────
    function getAppInfo(string calldata appId) external view returns (AppInfo memory) {
        return AppInfo({
            composeHash:  "",
            volumesHash:  "",
            imageHashes:  appImageHashes[appId],
            owner:        appOwner[appId],
            registeredAt: appRegisteredAt[appId]
        });
    }
    function getNode(string calldata appId, address signer) external view returns (NodeInfo memory) {
        return NodeInfo({teeUrl: "", addedAt: nodeAddedAt[appId][signer], stakeAmount: 0});
    }
    function isAcknowledged(address user, string calldata appId) external view returns (bool) {
        return userAcked[user][appId];
    }
    function isAuthorizedInvalidator(string calldata appId, address inv) external view returns (bool) {
        return invalidators[appId][inv];
    }
    function invalidateAcks(string calldata /* appId */) external {
        // Test-side observable: number of invalidate calls. We don't actually
        // clear acks here because tests set them explicitly per case.
        invalidateCount++;
    }
}

contract SandboxServingTest is Test {
    SandboxServing public serving;
    MockTappRegistry public tap;

    address user     = makeAddr("user");
    address appOwner = makeAddr("appOwner");

    // TEE signing key of node #1 (deterministic, tests only). Its address IS
    // the provider: voucher payee, services key, balance bucket, earnings key.
    uint256 constant TEE_PRIV = 0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80;
    address provider; // = vm.addr(TEE_PRIV)

    // Second node's TEE key, for multi-node / cross-signing cases.
    uint256 constant TEE2_PRIV = 0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d;
    address provider2; // = vm.addr(TEE2_PRIV)

    string constant APP_ID = "sandbox-test-app";

    bytes32 constant VOUCHER_TYPEHASH = keccak256(
        "SandboxVoucher(address user,address provider,bytes32 usageHash,uint256 nonce,uint256 totalFee)"
    );

    function setUp() public {
        tap = new MockTappRegistry();

        SandboxServing impl = new SandboxServing();
        UpgradeableBeacon beacon = new UpgradeableBeacon(address(impl), address(this));
        bytes memory initData = abi.encodeCall(SandboxServing.initialize, (address(tap)));
        BeaconProxy proxy = new BeaconProxy(address(beacon), initData);
        serving = SandboxServing(payable(address(proxy)));

        provider  = vm.addr(TEE_PRIV);
        provider2 = vm.addr(TEE2_PRIV);

        vm.deal(user, 10 ether);
        vm.deal(appOwner, 10 ether);

        // App owner's 4-step registration ceremony (collapsed for tests):
        // registerApp + addNode + authorizeInvalidator in TappRegistry,
        // then addOrUpdateService(signer, ...) here.
        tap.setAppOwner(APP_ID, appOwner);
        tap.addNode(APP_ID, provider);
        tap.authorize(APP_ID, address(serving));

        vm.prank(appOwner);
        serving.addOrUpdateService(
            provider,
            "https://provider.example.com",
            APP_ID,
            1000,   // pricePerCPUPerMin
            5000,   // createFee
            500     // pricePerMemGBPerMin
        );
    }

    // ── Deposit / Refund (untouched by the migration) ────────────────────────

    function test_Deposit() public {
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        (uint256 bal,,) = serving.getBalance(user, provider);
        assertEq(bal, 1 ether);
    }

    function test_Deposit_ThirdParty() public {
        address payer = makeAddr("payer");
        vm.deal(payer, 5 ether);
        vm.prank(payer);
        serving.deposit{value: 2 ether}(user, provider);
        (uint256 bal,,) = serving.getBalance(user, provider);
        assertEq(bal, 2 ether);
    }

    function test_RequestRefund_ThenWithdraw() public {
        vm.startPrank(user);
        serving.deposit{value: 1 ether}(user, provider);
        serving.requestRefund(provider, 0.5 ether);
        vm.stopPrank();

        (uint256 bal, uint256 pending,) = serving.getBalance(user, provider);
        assertEq(bal, 0.5 ether);
        assertEq(pending, 0.5 ether);

        vm.warp(block.timestamp + 2 hours + 1);

        uint256 before = user.balance;
        vm.prank(user);
        serving.withdrawRefund(provider);
        assertEq(user.balance - before, 0.5 ether);
    }

    function test_RequestRefund_Locked() public {
        vm.startPrank(user);
        serving.deposit{value: 1 ether}(user, provider);
        serving.requestRefund(provider, 0.5 ether);
        vm.expectRevert("refund locked");
        serving.withdrawRefund(provider);
        vm.stopPrank();
    }

    function test_RequestRefund_ReplacesPrevious() public {
        vm.startPrank(user);
        serving.deposit{value: 2 ether}(user, provider);
        serving.requestRefund(provider, 1 ether);
        serving.requestRefund(provider, 0.5 ether);
        vm.stopPrank();

        (uint256 bal, uint256 pending,) = serving.getBalance(user, provider);
        assertEq(bal, 1.5 ether);
        assertEq(pending, 0.5 ether);
    }

    /// Rotation path: after a machine rebuild the old signer's bucket is
    /// drained via the normal refund flow — no service entry required.
    function test_Refund_WorksAfterServiceRemoved() public {
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);

        vm.prank(appOwner);
        serving.removeService(provider);

        vm.prank(user);
        serving.requestRefund(provider, 1 ether);
        vm.warp(block.timestamp + 2 hours + 1);
        uint256 before = user.balance;
        vm.prank(user);
        serving.withdrawRefund(provider);
        assertEq(user.balance - before, 1 ether);
    }

    // ── Settlement ──────────────────────────────────────────────────────────

    function _makeVoucherSignedBy(
        uint256 privKey,
        address _user,
        address _provider,
        uint256 totalFee,
        bytes32 usageHash,
        uint256 nonce
    ) internal view returns (SandboxServing.SandboxVoucher memory) {
        bytes32 structHash = keccak256(abi.encode(
            VOUCHER_TYPEHASH, _user, _provider, usageHash, nonce, totalFee
        ));
        bytes32 digest = keccak256(abi.encodePacked(
            "\x19\x01", serving.domainSeparator(), structHash
        ));
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(privKey, digest);
        bytes memory sig = abi.encodePacked(r, s, v);

        return SandboxServing.SandboxVoucher({
            user:      _user,
            provider:  _provider,
            totalFee:  totalFee,
            usageHash: usageHash,
            nonce:     nonce,
            signature: sig
        });
    }

    function _makeVoucher(
        address _user,
        address _provider,
        uint256 totalFee,
        bytes32 usageHash,
        uint256 nonce
    ) internal view returns (SandboxServing.SandboxVoucher memory) {
        return _makeVoucherSignedBy(TEE_PRIV, _user, _provider, totalFee, usageHash, nonce);
    }

    function _settle(SandboxServing.SandboxVoucher memory v)
        internal
        returns (SandboxServing.SettlementStatus)
    {
        SandboxServing.SandboxVoucher[] memory vs = new SandboxServing.SandboxVoucher[](1);
        vs[0] = v;
        SandboxServing.SettlementStatus[] memory statuses = serving.settleFeesWithTEE(vs);
        return statuses[0];
    }

    function test_Settle_Success() public {
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        tap.setAck(user, APP_ID, true);

        SandboxServing.SettlementStatus status = _settle(
            _makeVoucher(user, provider, 1000, keccak256("usage1"), 1)
        );
        assertEq(uint8(status), uint8(SandboxServing.SettlementStatus.SUCCESS));
        assertEq(serving.getProviderEarnings(provider), 1000);
        (uint256 bal,,) = serving.getBalance(user, provider);
        assertEq(bal, 1 ether - 1000);
    }

    function test_Settle_InsufficientBalance() public {
        vm.prank(user);
        serving.deposit{value: 100}(user, provider);
        tap.setAck(user, APP_ID, true);

        SandboxServing.SettlementStatus status = _settle(
            _makeVoucher(user, provider, 1000, keccak256("usage2"), 1)
        );
        assertEq(uint8(status), uint8(SandboxServing.SettlementStatus.INSUFFICIENT_BALANCE));
        (uint256 bal, uint256 pending,) = serving.getBalance(user, provider);
        assertEq(bal, 0);
        assertEq(pending, 0);
        assertEq(serving.getProviderEarnings(provider), 100);
    }

    function test_Settle_NotAcknowledged() public {
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        // No tap.setAck — ack stays false

        SandboxServing.SettlementStatus status = _settle(
            _makeVoucher(user, provider, 1000, keccak256("usage3"), 1)
        );
        assertEq(uint8(status), uint8(SandboxServing.SettlementStatus.NOT_ACKNOWLEDGED));
    }

    function test_Settle_InvalidNonce() public {
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        tap.setAck(user, APP_ID, true);

        _settle(_makeVoucher(user, provider, 100, keccak256("u1"), 1));
        SandboxServing.SettlementStatus status = _settle(
            _makeVoucher(user, provider, 100, keccak256("u1"), 1)
        );
        assertEq(uint8(status), uint8(SandboxServing.SettlementStatus.INVALID_NONCE));
    }

    function test_Settle_InvalidSignature_TamperedFee() public {
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        tap.setAck(user, APP_ID, true);

        SandboxServing.SandboxVoucher memory v = _makeVoucher(user, provider, 1000, keccak256("u1"), 1);
        v.totalFee = 999999;
        SandboxServing.SettlementStatus status = _settle(v);
        assertEq(uint8(status), uint8(SandboxServing.SettlementStatus.INVALID_SIGNATURE));
    }

    function test_Settle_InvalidSignature_StrangerKey() public {
        // Correct payee, but signed by a key that is neither the payee nor a node.
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        tap.setAck(user, APP_ID, true);

        uint256 strangerPriv = 0x1111111111111111111111111111111111111111111111111111111111111111;
        SandboxServing.SandboxVoucher memory bad =
            _makeVoucherSignedBy(strangerPriv, user, provider, 100, keccak256("u-stranger"), 1);

        SandboxServing.SettlementStatus status = _settle(bad);
        assertEq(uint8(status), uint8(SandboxServing.SettlementStatus.INVALID_SIGNATURE));
    }

    /// v2 core property: a voucher must be signed BY its own payee. Node #1
    /// signing a voucher that names node #2 as payee is rejected even though
    /// node #1 is an active node of the same app.
    function test_Settle_CrossSigningRejected() public {
        tap.addNode(APP_ID, provider2);
        vm.prank(appOwner);
        serving.addOrUpdateService(provider2, "https://node2.example.com", APP_ID, 1000, 5000, 500);

        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider2);
        tap.setAck(user, APP_ID, true);

        // Payee is provider2 but signature comes from node #1's key.
        SandboxServing.SandboxVoucher memory cross =
            _makeVoucherSignedBy(TEE_PRIV, user, provider2, 1000, keccak256("cross"), 1);
        SandboxServing.SettlementStatus status = _settle(cross);
        assertEq(uint8(status), uint8(SandboxServing.SettlementStatus.INVALID_SIGNATURE));

        // Signed by its own payee → settles.
        SandboxServing.SandboxVoucher memory good =
            _makeVoucherSignedBy(TEE2_PRIV, user, provider2, 1000, keccak256("own"), 1);
        assertEq(uint8(_settle(good)), uint8(SandboxServing.SettlementStatus.SUCCESS));
        assertEq(serving.getProviderEarnings(provider2), 1000);
    }

    function test_Settle_RemovedNodeRejected() public {
        // Signer was a node when the service was registered, then got removed
        // from TappRegistry (remove-node-onchain). Its vouchers must stop settling.
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        tap.setAck(user, APP_ID, true);

        tap.removeNode(APP_ID, provider);
        SandboxServing.SettlementStatus status = _settle(
            _makeVoucher(user, provider, 100, keccak256("late"), 1)
        );
        assertEq(uint8(status), uint8(SandboxServing.SettlementStatus.INVALID_SIGNATURE));
    }

    function test_Settle_ProviderMismatch() public {
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        tap.setAck(user, APP_ID, true);

        address attacker = makeAddr("attacker");
        SandboxServing.SandboxVoucher memory v = _makeVoucher(user, attacker, 1000, keccak256("u1"), 1);
        SandboxServing.SandboxVoucher[] memory vs = new SandboxServing.SandboxVoucher[](1);
        vs[0] = v;
        SandboxServing.SettlementStatus[] memory statuses = serving.settleFeesWithTEE(vs);
        assertEq(uint8(statuses[0]), uint8(SandboxServing.SettlementStatus.PROVIDER_MISMATCH));
    }

    function test_Settle_LIFOInvariant() public {
        vm.startPrank(user);
        serving.deposit{value: 1000}(user, provider);
        serving.requestRefund(provider, 500);
        vm.stopPrank();
        tap.setAck(user, APP_ID, true);

        _settle(_makeVoucher(user, provider, 400, keccak256("u1"), 1));

        (uint256 bal, uint256 pending,) = serving.getBalance(user, provider);
        assertEq(bal, 100);
        assertEq(pending, 100);
        assertEq(serving.getProviderEarnings(provider), 400);
    }

    // ── Earnings ─────────────────────────────────────────────────────────────

    function test_WithdrawEarnings_PaysAppOwner() public {
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        tap.setAck(user, APP_ID, true);

        _settle(_makeVoucher(user, provider, 5000, keccak256("u1"), 1));

        uint256 before = appOwner.balance;
        vm.prank(appOwner);
        serving.withdrawEarnings(provider);
        assertEq(appOwner.balance - before, 5000);
        assertEq(serving.getProviderEarnings(provider), 0);
    }

    function test_WithdrawEarnings_RejectsNonAppOwner() public {
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        tap.setAck(user, APP_ID, true);
        _settle(_makeVoucher(user, provider, 5000, keccak256("u1"), 1));

        address impostor = makeAddr("impostor");
        vm.expectRevert("not app owner");
        vm.prank(impostor);
        serving.withdrawEarnings(provider);

        // The signer address itself has no special payout right either.
        vm.deal(provider, 1 ether);
        vm.expectRevert("not app owner");
        vm.prank(provider);
        serving.withdrawEarnings(provider);
    }

    function test_WithdrawEarnings_UnknownSignerRejected() public {
        address unknown = makeAddr("unknown");
        vm.expectRevert("not app owner");
        vm.prank(appOwner);
        serving.withdrawEarnings(unknown);
    }

    // ── Service registration ─────────────────────────────────────────────────

    function test_AddService_RejectsNonAppOwner() public {
        address impostor = makeAddr("impostor");
        vm.expectRevert("not app owner");
        vm.prank(impostor);
        serving.addOrUpdateService(provider, "u", APP_ID, 1, 1, 1);
    }

    function test_AddService_RejectsNonNodeSigner() public {
        // Signer not registered as a node of the appId in TappRegistry.
        address ghost = makeAddr("ghost");
        vm.expectRevert("signer not an active node");
        vm.prank(appOwner);
        serving.addOrUpdateService(ghost, "u", APP_ID, 1, 1, 1);
    }

    function test_AddService_RejectsWhenNotAuthorized() public {
        // A different app whose owner never authorized us as invalidator.
        string memory appId2 = "another-app";
        address owner2 = makeAddr("owner2");
        address signer2 = makeAddr("signer2");
        tap.setAppOwner(appId2, owner2);
        tap.addNode(appId2, signer2);
        // No tap.authorize(appId2, serving)
        vm.expectRevert("sandbox not authorized as invalidator");
        vm.prank(owner2);
        serving.addOrUpdateService(signer2, "u", appId2, 1, 1, 1);
    }

    function test_UpdateService_AppIdImmutable() public {
        // Try to rebind the same signer to a different appId — must revert.
        string memory appId2 = "another-app";
        tap.setAppOwner(appId2, appOwner);
        tap.addNode(appId2, provider);
        tap.authorize(appId2, address(serving));
        vm.expectRevert("appId immutable; remove to change");
        vm.prank(appOwner);
        serving.addOrUpdateService(provider, "u", appId2, 1, 1, 1);
    }

    function test_UpdateService_PriceChangeInvalidatesAcks() public {
        uint256 before = tap.invalidateCount();
        vm.prank(appOwner);
        serving.addOrUpdateService(provider, "u", APP_ID, 9999, 5000, 500); // CPU price changed
        assertEq(tap.invalidateCount(), before + 1);
    }

    function test_UpdateService_UrlChangeDoesNotInvalidate() public {
        uint256 before = tap.invalidateCount();
        vm.prank(appOwner);
        serving.addOrUpdateService(provider, "new-url", APP_ID, 1000, 5000, 500); // only URL changed
        assertEq(tap.invalidateCount(), before, "URL change must not invalidate acks");
    }

    /// One appId, many nodes: each signer gets its own isolated service entry,
    /// balance bucket, nonce stream, and earnings — registered by the same owner.
    function test_MultiNode_IsolatedLedgers() public {
        tap.addNode(APP_ID, provider2);
        vm.prank(appOwner);
        serving.addOrUpdateService(provider2, "https://node2.example.com", APP_ID, 2000, 6000, 700);

        assertTrue(serving.serviceExists(provider2));
        (, string memory appId2,,,) = serving.services(provider2);
        assertEq(appId2, APP_ID);

        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider2);
        tap.setAck(user, APP_ID, true);

        SandboxServing.SandboxVoucher memory v =
            _makeVoucherSignedBy(TEE2_PRIV, user, provider2, 1000, keccak256("node2-usage"), 1);
        assertEq(uint8(_settle(v)), uint8(SandboxServing.SettlementStatus.SUCCESS));

        assertEq(serving.getProviderEarnings(provider2), 1000);
        assertEq(serving.getProviderEarnings(provider), 0); // node1's ledger untouched
        (uint256 bal2,,) = serving.getBalance(user, provider2);
        (uint256 bal1,,) = serving.getBalance(user, provider);
        assertEq(bal2, 1 ether - 1000);
        assertEq(bal1, 0); // no shared pool
    }

    // ── removeService ─────────────────────────────────────────────────────────

    function test_RemoveService_SweepsEarningsAndBlocksSettlement() public {
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        tap.setAck(user, APP_ID, true);
        _settle(_makeVoucher(user, provider, 5000, keccak256("u1"), 1));
        assertEq(serving.getProviderEarnings(provider), 5000);

        uint256 before = appOwner.balance;
        vm.prank(appOwner);
        serving.removeService(provider);

        // Pending earnings swept to the owner in the same call.
        assertEq(appOwner.balance - before, 5000);
        assertEq(serving.getProviderEarnings(provider), 0);
        assertFalse(serving.serviceExists(provider));

        // No further settlement against the removed signer.
        SandboxServing.SettlementStatus status = _settle(
            _makeVoucher(user, provider, 100, keccak256("u2"), 2)
        );
        assertEq(uint8(status), uint8(SandboxServing.SettlementStatus.PROVIDER_MISMATCH));

        // User balance is preserved and refundable (see test_Refund_WorksAfterServiceRemoved).
        (uint256 bal,,) = serving.getBalance(user, provider);
        assertEq(bal, 1 ether - 5000);
    }

    function test_RemoveService_RejectsNonAppOwner() public {
        address impostor = makeAddr("impostor");
        vm.expectRevert("not app owner");
        vm.prank(impostor);
        serving.removeService(provider);
    }

    function test_RemoveService_ThenReRegisterKeepsNonces() public {
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        tap.setAck(user, APP_ID, true);
        _settle(_makeVoucher(user, provider, 100, keccak256("u1"), 7));

        vm.prank(appOwner);
        serving.removeService(provider);
        vm.prank(appOwner);
        serving.addOrUpdateService(provider, "u", APP_ID, 1000, 5000, 500);

        // Old voucher can't be replayed: nonce watermark survived the remove.
        SandboxServing.SettlementStatus status = _settle(
            _makeVoucher(user, provider, 100, keccak256("u1"), 7)
        );
        assertEq(uint8(status), uint8(SandboxServing.SettlementStatus.INVALID_NONCE));
        assertEq(serving.getLastNonce(user, provider), 7);
    }

    function test_IsTEEAcknowledged_Shim() public {
        assertFalse(serving.isTEEAcknowledged(user, provider));
        tap.setAck(user, APP_ID, true);
        assertTrue(serving.isTEEAcknowledged(user, provider));
    }

    function test_IsTEEAcknowledged_UnknownProviderReturnsFalse() public {
        address unknown = makeAddr("unknown");
        assertFalse(serving.isTEEAcknowledged(user, unknown));
    }

    // ── KMS providers ────────────────────────────────────────────────────────

    // The app's KMS key (GetSecretResource): the same on every boot, never a node.
    uint256 constant KMS_PRIV = 0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a;

    function _kmsProvider() internal pure returns (address) {
        return vm.addr(KMS_PRIV);
    }

    function _endorse(uint256 nodePriv, address kmsProvider, string memory appId) internal view returns (bytes memory) {
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(nodePriv, serving.kmsProviderDigest(kmsProvider, appId));
        return abi.encodePacked(r, s, v);
    }

    /// Owner registers the KMS provider, endorsed by node #1, and a user funds it.
    function _registerKms() internal returns (address kmsProvider) {
        kmsProvider = _kmsProvider();
        bytes memory endorsement = _endorse(TEE_PRIV, kmsProvider, APP_ID); // before the prank: it calls serving
        vm.prank(appOwner);
        serving.addOrUpdateKmsService(
            kmsProvider, "https://provider.example.com", APP_ID, 1000, 5000, 500, endorsement
        );
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, kmsProvider);
        tap.setAck(user, APP_ID, true);
    }

    function test_Kms_RegisterAndSettle() public {
        address kmsProvider = _registerKms();
        (uint256 epoch, bytes32 appIdHash) = serving.kmsBindings(kmsProvider);
        assertEq(epoch, tap.appRegisteredAt(APP_ID));
        assertEq(appIdHash, keccak256(bytes(APP_ID)));
        assertTrue(serving.isActiveProvider(kmsProvider));

        SandboxServing.SandboxVoucher memory v =
            _makeVoucherSignedBy(KMS_PRIV, user, kmsProvider, 1000, keccak256("k1"), 1);
        assertEq(uint8(_settle(v)), uint8(SandboxServing.SettlementStatus.SUCCESS));
        assertEq(serving.getProviderEarnings(kmsProvider), 1000);
    }

    /// The point of a KMS provider: the node that endorsed it reboots into a
    /// new signer, and the provider — its balances, nonces, service — carries on.
    function test_Kms_SurvivesNodeRotation() public {
        address kmsProvider = _registerKms();
        tap.addNode(APP_ID, provider2);
        tap.removeNode(APP_ID, provider);

        SandboxServing.SandboxVoucher memory v =
            _makeVoucherSignedBy(KMS_PRIV, user, kmsProvider, 1000, keccak256("after-reboot"), 1);
        assertEq(uint8(_settle(v)), uint8(SandboxServing.SettlementStatus.SUCCESS));
        assertTrue(serving.isActiveProvider(kmsProvider));
    }

    function test_Kms_VoucherSignedByNodeRejected() public {
        address kmsProvider = _registerKms();
        // An active node signing for the KMS provider is not the provider.
        SandboxServing.SandboxVoucher memory v =
            _makeVoucherSignedBy(TEE_PRIV, user, kmsProvider, 1000, keccak256("node-signs"), 1);
        assertEq(uint8(_settle(v)), uint8(SandboxServing.SettlementStatus.INVALID_SIGNATURE));
    }

    function test_Kms_EndorsementFromNonNodeRejected() public {
        address kmsProvider = _kmsProvider();
        uint256 strangerPriv = 0x1111111111111111111111111111111111111111111111111111111111111111;
        bytes memory endorsement = _endorse(strangerPriv, kmsProvider, APP_ID);
        vm.expectRevert("endorsement not from an active node");
        vm.prank(appOwner);
        serving.addOrUpdateKmsService(kmsProvider, "u", APP_ID, 1000, 5000, 500, endorsement);
    }

    function test_Kms_EndorsementForAnotherProviderRejected() public {
        // A node's endorsement of one key does not register another.
        address other = makeAddr("other");
        bytes memory endorsement = _endorse(TEE_PRIV, _kmsProvider(), APP_ID);
        vm.expectRevert("endorsement not from an active node");
        vm.prank(appOwner);
        serving.addOrUpdateKmsService(other, "u", APP_ID, 1000, 5000, 500, endorsement);
    }

    function test_Kms_RejectsNonAppOwner() public {
        address kmsProvider = _kmsProvider();
        bytes memory endorsement = _endorse(TEE_PRIV, kmsProvider, APP_ID);
        vm.expectRevert("not app owner");
        vm.prank(makeAddr("impostor"));
        serving.addOrUpdateKmsService(kmsProvider, "u", APP_ID, 1000, 5000, 500, endorsement);
    }

    function test_Kms_PathsDoNotMix() public {
        address kmsProvider = _registerKms();
        // A KMS provider cannot be updated through the node-signer path, which
        // skips the endorsement and the app-instance check.
        vm.expectRevert("kms provider; use addOrUpdateKmsService");
        vm.prank(appOwner);
        serving.addOrUpdateService(kmsProvider, "u", APP_ID, 1000, 5000, 500);

        // A node-signer service cannot be turned into a KMS one in place.
        bytes memory endorsement = _endorse(TEE_PRIV, provider, APP_ID);
        vm.expectRevert("node-signer service; remove it first");
        vm.prank(appOwner);
        serving.addOrUpdateKmsService(provider, "u", APP_ID, 1000, 5000, 500, endorsement);
    }

    function test_Kms_PriceChangeInvalidatesAcks() public {
        address kmsProvider = _registerKms();
        uint256 before = tap.invalidateCount();
        bytes memory endorsement = _endorse(TEE_PRIV, kmsProvider, APP_ID);
        vm.prank(appOwner);
        serving.addOrUpdateKmsService(kmsProvider, "u2", APP_ID, 2000, 5000, 500, endorsement);
        assertEq(tap.invalidateCount(), before + 1);
    }

    /// The appId's last node removed, someone registers it again and gets the
    /// same KMS key. The provider bound to the earlier instance stays theirs to
    /// neither settle against nor manage.
    function test_Kms_ReRegisteredAppLockedOut() public {
        address kmsProvider = _registerKms();
        _settle(_makeVoucherSignedBy(KMS_PRIV, user, kmsProvider, 1000, keccak256("k1"), 1));

        address squatter = makeAddr("squatter");
        uint256 squatterNodePriv = 0x2222222222222222222222222222222222222222222222222222222222222222;
        tap.removeNode(APP_ID, provider);
        tap.reRegister(APP_ID, squatter);
        tap.addNode(APP_ID, vm.addr(squatterNodePriv));
        tap.setAck(user, APP_ID, true); // even with the user's ack

        SandboxServing.SandboxVoucher memory v =
            _makeVoucherSignedBy(KMS_PRIV, user, kmsProvider, 1000, keccak256("squat"), 2);
        assertEq(uint8(_settle(v)), uint8(SandboxServing.SettlementStatus.INVALID_SIGNATURE));
        assertFalse(serving.isActiveProvider(kmsProvider));

        vm.startPrank(squatter);
        vm.expectRevert("not app owner");
        serving.withdrawEarnings(kmsProvider);
        vm.expectRevert("not app owner");
        serving.removeService(kmsProvider);
        bytes memory endorsement = _endorse(squatterNodePriv, kmsProvider, APP_ID);
        vm.expectRevert("app re-registered since the provider was bound");
        serving.addOrUpdateKmsService(kmsProvider, "u", APP_ID, 1, 1, 1, endorsement);
        vm.stopPrank();

        (uint256 bal,,) = serving.getBalance(user, kmsProvider);
        assertEq(bal, 1 ether - 1000);
        assertEq(serving.getProviderEarnings(kmsProvider), 1000);
    }

    function test_Kms_RemoveKeepsBinding() public {
        address kmsProvider = _registerKms();
        vm.prank(appOwner);
        serving.removeService(kmsProvider);
        (uint256 epoch,) = serving.kmsBindings(kmsProvider);
        assertEq(epoch, tap.appRegisteredAt(APP_ID));

        // Same instance: the owner can bring it back.
        bytes memory endorsement = _endorse(TEE_PRIV, kmsProvider, APP_ID);
        vm.prank(appOwner);
        serving.addOrUpdateKmsService(kmsProvider, "u", APP_ID, 1000, 5000, 500, endorsement);
        assertTrue(serving.isActiveProvider(kmsProvider));

        // Under another appId: no.
        string memory otherApp = "other-app";
        tap.setAppOwner(otherApp, appOwner);
        tap.addNode(otherApp, provider);
        tap.authorize(otherApp, address(serving));
        vm.prank(appOwner);
        serving.removeService(kmsProvider);
        bytes memory otherEndorsement = _endorse(TEE_PRIV, kmsProvider, otherApp);
        vm.expectRevert("kms provider bound to another app");
        vm.prank(appOwner);
        serving.addOrUpdateKmsService(kmsProvider, "u", otherApp, 1000, 5000, 500, otherEndorsement);
    }

    /// TappRegistry redeployed: the app's new instance has a new registeredAt,
    /// and only the contract owner can move the binding to it.
    function test_Kms_RepinAfterRegistryRedeploy() public {
        address kmsProvider = _registerKms();
        MockTappRegistry tap2 = new MockTappRegistry();
        vm.warp(block.timestamp + 100);
        tap2.setAppOwner(APP_ID, appOwner);
        tap2.addNode(APP_ID, provider);
        tap2.setAck(user, APP_ID, true);
        serving.setTappRegistry(address(tap2));
        assertFalse(serving.isActiveProvider(kmsProvider));

        vm.expectRevert("not owner");
        vm.prank(appOwner);
        serving.repinKmsProvider(kmsProvider);

        serving.repinKmsProvider(kmsProvider);
        assertTrue(serving.isActiveProvider(kmsProvider));
        SandboxServing.SandboxVoucher memory v =
            _makeVoucherSignedBy(KMS_PRIV, user, kmsProvider, 1000, keccak256("repinned"), 1);
        assertEq(uint8(_settle(v)), uint8(SandboxServing.SettlementStatus.SUCCESS));

        vm.expectRevert("not a kms provider");
        serving.repinKmsProvider(provider);
    }

    /// The per-batch provider memo must key on the provider: a batch mixing a
    /// node-signer provider and a KMS provider checks each by its own rule.
    function test_Kms_MixedBatch() public {
        address kmsProvider = _registerKms();
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);

        SandboxServing.SandboxVoucher[] memory vs = new SandboxServing.SandboxVoucher[](4);
        vs[0] = _makeVoucherSignedBy(KMS_PRIV, user, kmsProvider, 100, keccak256("a"), 1);
        vs[1] = _makeVoucher(user, provider, 100, keccak256("b"), 1);
        vs[2] = _makeVoucherSignedBy(KMS_PRIV, user, kmsProvider, 100, keccak256("c"), 2);
        // node #1 signing for the KMS provider, right after a valid one: the memo
        // must not let it through.
        vs[3] = _makeVoucherSignedBy(TEE_PRIV, user, kmsProvider, 100, keccak256("d"), 3);
        SandboxServing.SettlementStatus[] memory st = serving.settleFeesWithTEE(vs);
        assertEq(uint8(st[0]), uint8(SandboxServing.SettlementStatus.SUCCESS));
        assertEq(uint8(st[1]), uint8(SandboxServing.SettlementStatus.SUCCESS));
        assertEq(uint8(st[2]), uint8(SandboxServing.SettlementStatus.SUCCESS));
        assertEq(uint8(st[3]), uint8(SandboxServing.SettlementStatus.INVALID_SIGNATURE));

        tap.removeNode(APP_ID, provider);
        SandboxServing.SandboxVoucher[] memory vs2 = new SandboxServing.SandboxVoucher[](2);
        vs2[0] = _makeVoucherSignedBy(KMS_PRIV, user, kmsProvider, 100, keccak256("e"), 4);
        vs2[1] = _makeVoucher(user, provider, 100, keccak256("f"), 2);
        SandboxServing.SettlementStatus[] memory st2 = serving.previewSettlementResults(vs2);
        assertEq(uint8(st2[0]), uint8(SandboxServing.SettlementStatus.SUCCESS));
        assertEq(uint8(st2[1]), uint8(SandboxServing.SettlementStatus.INVALID_SIGNATURE));
    }

    /// Pins the digest Go computes (internal/voucher TestKmsProviderDigestVector)
    /// for the same chain, contract, provider and appId.
    function test_Kms_DigestVector() public view {
        assertEq(block.chainid, 31337);
        assertEq(address(serving), 0x5991A2dF15A8F6A256D3Ec51E99254Cd3fb576A9);
        assertEq(_kmsProvider(), 0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC);
        assertEq(
            serving.kmsProviderDigest(_kmsProvider(), APP_ID),
            0xf1a05d887d3bfa532429d95d0e9e270da85bc96e28217f12c54518beee1e3cb7
        );
    }

    function test_IsActiveProvider_NodeSigner() public {
        assertTrue(serving.isActiveProvider(provider));
        tap.removeNode(APP_ID, provider);
        assertFalse(serving.isActiveProvider(provider));
        assertFalse(serving.isActiveProvider(makeAddr("unknown")));
    }

    /// Gas of one KMS-provider settlement against an app with 13 images (the
    /// sandbox compose), batched vs not — getAppInfo is read once per batch.
    function test_Kms_SettlementGas() public {
        address kmsProvider = _registerKms();
        tap.setImageHashes(APP_ID, 13);

        SandboxServing.SandboxVoucher[] memory one = new SandboxServing.SandboxVoucher[](1);
        one[0] = _makeVoucherSignedBy(KMS_PRIV, user, kmsProvider, 100, keccak256("g1"), 1);
        uint256 g = gasleft();
        serving.settleFeesWithTEE(one);
        console.log("kms, 1 voucher:   ", g - gasleft());

        SandboxServing.SandboxVoucher[] memory ten = new SandboxServing.SandboxVoucher[](10);
        for (uint256 i = 0; i < 10; i++) {
            ten[i] = _makeVoucherSignedBy(KMS_PRIV, user, kmsProvider, 100, keccak256(abi.encode(i)), 2 + i);
        }
        g = gasleft();
        serving.settleFeesWithTEE(ten);
        console.log("kms, 10 vouchers: ", g - gasleft());

        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        SandboxServing.SandboxVoucher[] memory legacy = new SandboxServing.SandboxVoucher[](1);
        legacy[0] = _makeVoucher(user, provider, 100, keccak256("g2"), 1);
        g = gasleft();
        serving.settleFeesWithTEE(legacy);
        console.log("node signer, 1:   ", g - gasleft());
    }

    // ── Admin ────────────────────────────────────────────────────────────────

    function test_TransferOwnership() public {
        assertEq(serving.owner(), address(this));
        serving.transferOwnership(user);
        assertEq(serving.owner(), user);
    }

    function test_TransferOwnership_OnlyOwner() public {
        vm.prank(user);
        vm.expectRevert("not owner");
        serving.transferOwnership(user);
    }

    // ── Upgrade ──────────────────────────────────────────────────────────────

    function test_Upgrade_PreservesState() public {
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        (uint256 balBefore,,) = serving.getBalance(user, provider);
        assertEq(balBefore, 1 ether);

        SandboxServing newImpl = new SandboxServing();
        bytes32 beaconSlot = 0xa3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50;
        address beaconAddr = address(uint160(uint256(vm.load(address(serving), beaconSlot))));
        UpgradeableBeacon(beaconAddr).upgradeTo(address(newImpl));

        (uint256 balAfter,,) = serving.getBalance(user, provider);
        assertEq(balAfter, 1 ether);

        // tappRegistry pointer must survive the upgrade.
        assertEq(address(serving.tappRegistry()), address(tap));
    }
}
