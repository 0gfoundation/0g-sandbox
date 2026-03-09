// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Test, console} from "forge-std/Test.sol";
import {SandboxServing} from "./SandboxServing.sol";
import {UpgradeableBeacon} from "./proxy/UpgradeableBeacon.sol";
import {BeaconProxy} from "./proxy/BeaconProxy.sol";

contract SandboxServingTest is Test {
    SandboxServing public serving;

    uint256 constant PROVIDER_STAKE = 0.1 ether;

    // Test accounts
    address user     = makeAddr("user");
    address provider = makeAddr("provider");

    // TEE signing key (deterministic, for tests only)
    uint256 constant TEE_PRIV = 0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80;
    address teeSigner;

    // EIP-712 type hash (must match SandboxServing.sol)
    bytes32 constant VOUCHER_TYPEHASH = keccak256(
        "SandboxVoucher(address user,address provider,bytes32 usageHash,uint256 nonce,uint256 totalFee)"
    );

    function setUp() public {
        // Deploy impl (constructor locks itself)
        SandboxServing impl = new SandboxServing();

        // Deploy beacon with this test contract as owner
        UpgradeableBeacon beacon = new UpgradeableBeacon(address(impl), address(this));

        // Deploy proxy with initialize(PROVIDER_STAKE) calldata
        bytes memory initData = abi.encodeCall(SandboxServing.initialize, (PROVIDER_STAKE));
        BeaconProxy proxy = new BeaconProxy(address(beacon), initData);

        // Bind the SandboxServing interface to the proxy address
        serving = SandboxServing(payable(address(proxy)));

        teeSigner = vm.addr(TEE_PRIV);

        // Fund accounts
        vm.deal(user, 10 ether);
        vm.deal(provider, 10 ether);

        // Register provider
        vm.prank(provider);
        serving.addOrUpdateService{value: PROVIDER_STAKE}(
            "https://provider.example.com",
            teeSigner,
            1000,   // pricePerCPUPerMin
            5000,   // createFee
            500     // pricePerMemGBPerMin
        );
    }

    // ── Deposit / Refund ────────────────────────────────────────────────────

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

        // Warp past lock time
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
        // Replace with a new refund amount
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
        // Deposit + acknowledge
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        vm.prank(user);
        serving.acknowledgeTEESigner(provider, true);

        SandboxServing.SandboxVoucher memory v = _makeVoucher(
            user, provider, 1000, keccak256("usage1"), 1
        );
        SandboxServing.SettlementStatus status = _settle(v);
        assertEq(uint8(status), uint8(SandboxServing.SettlementStatus.SUCCESS));

        assertEq(serving.getProviderEarnings(provider), 1000);
        (uint256 bal,,) = serving.getBalance(user, provider);
        assertEq(bal, 1 ether - 1000);
    }

    function test_Settle_InsufficientBalance() public {
        vm.prank(user);
        serving.deposit{value: 100}(user, provider); // only 100 neuron
        vm.prank(user);
        serving.acknowledgeTEESigner(provider, true);

        SandboxServing.SandboxVoucher memory v = _makeVoucher(
            user, provider, 1000, keccak256("usage2"), 1
        );
        SandboxServing.SettlementStatus status = _settle(v);
        assertEq(uint8(status), uint8(SandboxServing.SettlementStatus.INSUFFICIENT_BALANCE));

        // All balance drained
        (uint256 bal, uint256 pending,) = serving.getBalance(user, provider);
        assertEq(bal, 0);
        assertEq(pending, 0);
        assertEq(serving.getProviderEarnings(provider), 100);
    }

    function test_Settle_NotAcknowledged() public {
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        // No acknowledgeTEESigner call

        SandboxServing.SandboxVoucher memory v = _makeVoucher(
            user, provider, 1000, keccak256("usage3"), 1
        );
        SandboxServing.SettlementStatus status = _settle(v);
        assertEq(uint8(status), uint8(SandboxServing.SettlementStatus.NOT_ACKNOWLEDGED));
    }

    function test_Settle_InvalidNonce() public {
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        vm.prank(user);
        serving.acknowledgeTEESigner(provider, true);

        // Settle nonce=1 first
        _settle(_makeVoucher(user, provider, 100, keccak256("u1"), 1));

        // Replay nonce=1
        SandboxServing.SettlementStatus status = _settle(
            _makeVoucher(user, provider, 100, keccak256("u1"), 1)
        );
        assertEq(uint8(status), uint8(SandboxServing.SettlementStatus.INVALID_NONCE));
    }

    function test_Settle_InvalidSignature() public {
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        vm.prank(user);
        serving.acknowledgeTEESigner(provider, true);

        SandboxServing.SandboxVoucher memory v = _makeVoucher(
            user, provider, 1000, keccak256("u1"), 1
        );
        // Tamper with the fee after signing
        v.totalFee = 999999;

        SandboxServing.SettlementStatus status = _settle(v);
        assertEq(uint8(status), uint8(SandboxServing.SettlementStatus.INVALID_SIGNATURE));
    }

    function test_Settle_ProviderMismatch() public {
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        vm.prank(user);
        serving.acknowledgeTEESigner(provider, true);

        SandboxServing.SandboxVoucher memory v = _makeVoucher(
            user, provider, 1000, keccak256("u1"), 1
        );
        // Different sender
        SandboxServing.SandboxVoucher[] memory vs = new SandboxServing.SandboxVoucher[](1);
        vs[0] = v;
        address attacker = makeAddr("attacker");
        vm.prank(attacker);
        SandboxServing.SettlementStatus[] memory statuses = serving.settleFeesWithTEE(vs);
        assertEq(uint8(statuses[0]), uint8(SandboxServing.SettlementStatus.PROVIDER_MISMATCH));
    }

    function test_Settle_LIFOInvariant() public {
        // user has 500 balance + 500 pendingRefund; voucher asks for 400
        vm.startPrank(user);
        serving.deposit{value: 1000}(user, provider);
        serving.requestRefund(provider, 500);
        serving.acknowledgeTEESigner(provider, true);
        vm.stopPrank();

        _settle(_makeVoucher(user, provider, 400, keccak256("u1"), 1));

        (uint256 bal, uint256 pending,) = serving.getBalance(user, provider);
        // bal was 500, paid 400 → bal = 100; pendingRefund must be ≤ bal
        assertEq(bal, 100);
        assertEq(pending, 100); // excess 400 absorbed from pendingRefund
        assertEq(serving.getProviderEarnings(provider), 400);
    }

    function test_WithdrawEarnings() public {
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        vm.prank(user);
        serving.acknowledgeTEESigner(provider, true);

        _settle(_makeVoucher(user, provider, 5000, keccak256("u1"), 1));

        uint256 before = provider.balance;
        vm.prank(provider);
        serving.withdrawEarnings();
        assertEq(provider.balance - before, 5000);
        assertEq(serving.getProviderEarnings(provider), 0);
    }

    // ── Provider management ─────────────────────────────────────────────────

    function test_AddService_RequiresStake() public {
        address newProvider = makeAddr("newprovider");
        vm.deal(newProvider, 1 ether);
        vm.prank(newProvider);
        vm.expectRevert("insufficient stake");
        serving.addOrUpdateService{value: 0.01 ether}("url", address(1), 1000, 5000, 500);
    }

    function test_UpdateService_NoStakeRequired() public {
        vm.prank(provider);
        serving.addOrUpdateService{value: 0}(
            "https://updated.example.com", teeSigner, 2000, 6000, 1000
        );
        // Verify it doesn't revert and service still exists
        assertTrue(serving.serviceExists(provider));
    }

    function test_AcknowledgeTEESigner() public {
        assertFalse(serving.isTEEAcknowledged(user, provider));
        vm.prank(user);
        serving.acknowledgeTEESigner(provider, true);
        assertTrue(serving.isTEEAcknowledged(user, provider));
        vm.prank(user);
        serving.acknowledgeTEESigner(provider, false);
        assertFalse(serving.isTEEAcknowledged(user, provider));
    }

    // ── Admin ────────────────────────────────────────────────────────────────

    function test_SetProviderStake() public {
        // Test contract is the owner (deployed + initialized the proxy in setUp)
        assertEq(serving.owner(), address(this));
        serving.setProviderStake(0.2 ether);
        assertEq(serving.providerStake(), 0.2 ether);
    }

    function test_SetProviderStake_OnlyOwner() public {
        vm.prank(user);
        vm.expectRevert("not owner");
        serving.setProviderStake(0.2 ether);
    }

    function test_TransferOwnership() public {
        serving.transferOwnership(user);
        assertEq(serving.owner(), user);
        // Previous owner can no longer call setProviderStake
        vm.expectRevert("not owner");
        serving.setProviderStake(0.3 ether);
    }

    function test_AddService_StakeReadFromContract() public {
        // providerStake is set to PROVIDER_STAKE (0.1 ether) in setUp
        // Sending exactly providerStake should succeed
        address newProvider = makeAddr("newprovider2");
        vm.deal(newProvider, 1 ether);
        vm.prank(newProvider);
        serving.addOrUpdateService{value: PROVIDER_STAKE}("url2", address(1), 1000, 5000, 500);
        assertTrue(serving.serviceExists(newProvider));
    }

    // ── Upgrade ──────────────────────────────────────────────────────────────

    function test_Upgrade_PreservesState() public {
        // Deposit some state
        vm.prank(user);
        serving.deposit{value: 1 ether}(user, provider);
        (uint256 balBefore,,) = serving.getBalance(user, provider);
        assertEq(balBefore, 1 ether);

        // Deploy a new impl and upgrade the beacon
        SandboxServing newImpl = new SandboxServing();
        // Get the beacon address from the proxy's ERC-1967 slot
        bytes32 beaconSlot = 0xa3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50;
        address beaconAddr = address(uint160(uint256(vm.load(address(serving), beaconSlot))));
        UpgradeableBeacon(beaconAddr).upgradeTo(address(newImpl));

        // State must be preserved
        (uint256 balAfter,,) = serving.getBalance(user, provider);
        assertEq(balAfter, 1 ether);
    }
}
