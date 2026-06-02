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
    address provider = makeAddr("provider");

    // TEE signing key (deterministic, for tests only)
    uint256 constant TEE_PRIV = 0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80;
    address teeSigner;

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

        teeSigner = vm.addr(TEE_PRIV);

        vm.deal(user, 10 ether);
        vm.deal(provider, 10 ether);

        // Provider's 3-step registration ceremony (here collapsed for tests).
        tap.setAppOwner(APP_ID, provider);
        tap.addNode(APP_ID, teeSigner);
        tap.authorize(APP_ID, address(serving));

        vm.prank(provider);
        serving.addOrUpdateService(
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

    // ── Settlement ──────────────────────────────────────────────────────────

    function _makeVoucher(
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
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(TEE_PRIV, digest);
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

    function _settle(SandboxServing.SandboxVoucher memory v)
        internal
        returns (SandboxServing.SettlementStatus)
    {
        SandboxServing.SandboxVoucher[] memory vs = new SandboxServing.SandboxVoucher[](1);
        vs[0] = v;
        vm.prank(provider);
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

    function test_Settle_InvalidSignature_UnknownSigner() public {
        // Voucher correctly signed by teeSigner, but tap no longer lists it as a node.
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        tap.setAck(user, APP_ID, true);

        // Remove the node from tap (set addedAt back to 0 by re-deploying mock state)
        // Instead, just submit a voucher signed by a different key:
        uint256 strangerPriv = 0xfeedbeef;
        address stranger = vm.addr(strangerPriv);
        tap.addNode(APP_ID, stranger); // need different node off — instead sign with stranger but stranger is also a node
        // Reset: sign with TEE_PRIV but remove that node by overwriting (set time=0 by direct slot manipulation is too brittle).
        // Cleaner: deploy a fresh mock with no nodes for this voucher's signer.
        MockTappRegistry tap2 = new MockTappRegistry();
        tap2.setAppOwner(APP_ID, provider);
        tap2.setAck(user, APP_ID, true);
        tap2.authorize(APP_ID, address(serving));
        // tap2 has NO addNode for teeSigner → getNode(...).addedAt == 0 → INVALID_SIGNATURE.
        // But serving is still bound to original tap; the easiest path is to verify the existing tap flow rejects unknown signers.
        stranger; tap2; // suppress unused warnings — this branch documents the design; the actual unknown-signer test is below.

        // Submit a voucher signed by strangerPriv — strangerPriv is not a node in tap.
        SandboxServing.SandboxVoucher memory bad = _makeVoucher(user, provider, 100, keccak256("u-stranger"), 1);
        // Override signature with stranger's key
        bytes32 structHash = keccak256(abi.encode(
            VOUCHER_TYPEHASH, user, provider, bad.usageHash, bad.nonce, bad.totalFee
        ));
        bytes32 digest = keccak256(abi.encodePacked("\x19\x01", serving.domainSeparator(), structHash));
        uint256 strangerPriv2 = 0x1111111111111111111111111111111111111111111111111111111111111111;
        (uint8 vv, bytes32 r, bytes32 s) = vm.sign(strangerPriv2, digest);
        bad.signature = abi.encodePacked(r, s, vv);

        SandboxServing.SettlementStatus status = _settle(bad);
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

    function test_WithdrawEarnings() public {
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        tap.setAck(user, APP_ID, true);

        _settle(_makeVoucher(user, provider, 5000, keccak256("u1"), 1));

        uint256 before = provider.balance;
        vm.prank(provider);
        serving.withdrawEarnings();
        assertEq(provider.balance - before, 5000);
        assertEq(serving.getProviderEarnings(provider), 0);
    }

    // ── Service registration ─────────────────────────────────────────────────

    function test_AddService_RejectsNonAppOwner() public {
        address impostor = makeAddr("impostor");
        // impostor does NOT own APP_ID in tap
        vm.expectRevert("not app owner");
        vm.prank(impostor);
        serving.addOrUpdateService("u", APP_ID, 1, 1, 1);
    }

    function test_AddService_RejectsWhenNotAuthorized() public {
        // Set up a new provider that owns a different app but never authorized us.
        string memory appId2 = "another-app";
        address p2 = makeAddr("p2");
        tap.setAppOwner(appId2, p2);
        // No tap.authorize(appId2, serving)
        vm.expectRevert("sandbox not authorized as invalidator");
        vm.prank(p2);
        serving.addOrUpdateService("u", appId2, 1, 1, 1);
    }

    function test_UpdateService_AppIdImmutable() public {
        // Try to switch to a different appId — must revert.
        string memory appId2 = "another-app";
        tap.setAppOwner(appId2, provider);
        tap.authorize(appId2, address(serving));
        vm.expectRevert("appId immutable; deregister to change");
        vm.prank(provider);
        serving.addOrUpdateService("u", appId2, 1, 1, 1);
    }

    function test_UpdateService_PriceChangeInvalidatesAcks() public {
        uint256 before = tap.invalidateCount();
        vm.prank(provider);
        serving.addOrUpdateService("u", APP_ID, 9999, 5000, 500); // CPU price changed
        assertEq(tap.invalidateCount(), before + 1);
    }

    function test_UpdateService_UrlChangeDoesNotInvalidate() public {
        uint256 before = tap.invalidateCount();
        vm.prank(provider);
        serving.addOrUpdateService("new-url", APP_ID, 1000, 5000, 500); // only URL changed
        assertEq(tap.invalidateCount(), before, "URL change must not invalidate acks");
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
