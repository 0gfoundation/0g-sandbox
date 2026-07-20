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
    uint256 public invalidateCount;

    function setAppOwner(string calldata appId, address ownerAddr) external {
        appOwner[appId] = ownerAddr;
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
        bytes[] memory empty;
        return AppInfo({composeHash: "", volumesHash: "", imageHashes: empty, owner: appOwner[appId], registeredAt: 0});
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
