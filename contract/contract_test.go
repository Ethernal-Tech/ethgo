package contract

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/Ethernal-Tech/ethgo"
	"github.com/Ethernal-Tech/ethgo/abi"
	"github.com/Ethernal-Tech/ethgo/jsonrpc"
	"github.com/Ethernal-Tech/ethgo/testutil"
	"github.com/Ethernal-Tech/ethgo/wallet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	addr0  = "0x0000000000000000000000000000000000000000"
	addr0B = ethgo.HexToAddress(addr0)
)

func TestContract_NoInput(t *testing.T) {
	s := testutil.NewTestServer(t)

	cc := &testutil.Contract{}
	cc.AddOutputCaller("set")

	contract, addr, err := s.DeployContract(cc)
	require.NoError(t, err)

	abi0, err := abi.NewABIFromSlice(contract.Abi)
	assert.NoError(t, err)

	p, _ := jsonrpc.NewClient(s.HTTPAddr())
	c := NewContract(addr, abi0, WithJsonRPC(p.Eth()))

	vals, err := c.Call("set", ethgo.Latest)
	assert.NoError(t, err)
	assert.Equal(t, vals["0"], big.NewInt(1))

	abi1, err := abi.NewABIFromList([]string{
		"function set() view returns (uint256)",
	})
	assert.NoError(t, err)

	c1 := NewContract(addr, abi1, WithJsonRPC(p.Eth()))
	vals, err = c1.Call("set", ethgo.Latest)
	assert.NoError(t, err)
	assert.Equal(t, vals["0"], big.NewInt(1))
}

func TestContract_IO(t *testing.T) {
	s := testutil.NewTestServer(t)

	cc := &testutil.Contract{}
	cc.AddDualCaller("setA", "address", "uint256")

	contract, addr, err := s.DeployContract(cc)
	require.NoError(t, err)

	abi, err := abi.NewABIFromSlice(contract.Abi)
	assert.NoError(t, err)

	c := NewContract(addr, abi, WithJsonRPCEndpoint(s.HTTPAddr()))

	resp, err := c.Call("setA", ethgo.Latest, addr0B, 1000)
	assert.NoError(t, err)

	assert.Equal(t, resp["0"], addr0B)
	assert.Equal(t, resp["1"], big.NewInt(1000))
}

func TestContract_From(t *testing.T) {
	s := testutil.NewTestServer(t)

	cc := &testutil.Contract{}
	cc.AddCallback(func() string {
		return `function example() public view returns (address) {
			return msg.sender;	
		}`
	})

	contract, addr, err := s.DeployContract(cc)
	require.NoError(t, err)

	abi, err := abi.NewABIFromSlice(contract.Abi)
	assert.NoError(t, err)

	from := ethgo.Address{0x1}
	c := NewContract(addr, abi, WithSender(from), WithJsonRPCEndpoint(s.HTTPAddr()))

	resp, err := c.Call("example", ethgo.Latest)
	assert.NoError(t, err)
	assert.Equal(t, resp["0"], from)
}

func TestContract_Deploy(t *testing.T) {
	s := testutil.NewTestServer(t)

	// create an address and fund it
	key, _ := wallet.GenerateKey()
	s.Fund(key.Address())

	p, _ := jsonrpc.NewClient(s.HTTPAddr())

	cc := &testutil.Contract{}
	cc.AddConstructor("address", "uint256")

	artifact, err := cc.Compile()
	assert.NoError(t, err)

	abi, err := abi.NewABIFromSlice(artifact.Abi)
	assert.NoError(t, err)

	bin, err := hex.DecodeString(artifact.Bin)
	assert.NoError(t, err)

	txn, err := DeployContract(abi, bin, []interface{}{ethgo.Address{0x1}, 1000}, WithJsonRPC(p.Eth()), WithSender(key))
	assert.NoError(t, err)

	assert.NoError(t, txn.Do())
	receipt, err := txn.Wait()
	assert.NoError(t, err)

	i := NewContract(receipt.ContractAddress, abi, WithJsonRPC(p.Eth()))
	resp, err := i.Call("val_0", ethgo.Latest)
	assert.NoError(t, err)
	assert.Equal(t, resp["0"], ethgo.Address{0x1})

	resp, err = i.Call("val_1", ethgo.Latest)
	assert.NoError(t, err)
	assert.Equal(t, resp["0"], big.NewInt(1000))
}

func TestContract_Transaction(t *testing.T) {
	s := testutil.NewTestServer(t)

	// create an address and fund it
	key, _ := wallet.GenerateKey()
	s.Fund(key.Address())

	cc := &testutil.Contract{}
	cc.AddEvent(testutil.NewEvent("A").Add("uint256", true))
	cc.EmitEvent("setA", "A", "1")

	artifact, addr, err := s.DeployContract(cc)
	require.NoError(t, err)

	abi, err := abi.NewABIFromSlice(artifact.Abi)
	assert.NoError(t, err)

	// send multiple transactions
	contract := NewContract(addr, abi, WithJsonRPCEndpoint(s.HTTPAddr()), WithSender(key))

	for i := 0; i < 10; i++ {
		txn, err := contract.Txn("setA")
		assert.NoError(t, err)

		err = txn.Do()
		assert.NoError(t, err)

		receipt, err := txn.Wait()
		assert.NoError(t, err)
		assert.Len(t, receipt.Logs, 1)
	}
}

func TestContract_CallAtBlock(t *testing.T) {
	s := testutil.NewTestServer(t)

	// create an address and fund it
	key, _ := wallet.GenerateKey()
	s.Fund(key.Address())

	cc := &testutil.Contract{}
	cc.AddCallback(func() string {
		return `
		uint256 val = 1;
		function getVal() public view returns (uint256) {
			return val;
		}
		function change() public payable {
			val = 2;
		}`
	})

	artifact, addr, err := s.DeployContract(cc)
	require.NoError(t, err)

	abi, err := abi.NewABIFromSlice(artifact.Abi)
	assert.NoError(t, err)

	contract := NewContract(addr, abi, WithJsonRPCEndpoint(s.HTTPAddr()), WithSender(key))

	checkVal := func(block ethgo.BlockNumber, expected *big.Int) {
		resp, err := contract.Call("getVal", block)
		assert.NoError(t, err)
		assert.Equal(t, resp["0"], expected)
	}

	// initial value is 1
	checkVal(ethgo.Latest, big.NewInt(1))

	// send a transaction to update the state
	var receipt *ethgo.Receipt
	{
		txn, err := contract.Txn("change")
		assert.NoError(t, err)

		err = txn.Do()
		assert.NoError(t, err)

		receipt, err = txn.Wait()
		assert.NoError(t, err)
	}

	// validate the state at different blocks
	{
		// value at receipt block is 2
		checkVal(ethgo.BlockNumber(receipt.BlockNumber), big.NewInt(2))

		// value at previous block is 1
		checkVal(ethgo.BlockNumber(receipt.BlockNumber-1), big.NewInt(1))
	}
}

func TestContract_SendValueContractCall(t *testing.T) {
	s := testutil.NewTestServer(t)

	key, _ := wallet.GenerateKey()
	s.Fund(key.Address())

	cc := &testutil.Contract{}
	cc.AddCallback(func() string {
		return `
		function deposit() public payable {
		}`
	})

	artifact, addr, err := s.DeployContract(cc)
	require.NoError(t, err)

	abi, err := abi.NewABIFromSlice(artifact.Abi)
	assert.NoError(t, err)

	contract := NewContract(addr, abi, WithJsonRPCEndpoint(s.HTTPAddr()), WithSender(key))

	balance := big.NewInt(1)

	txn, err := contract.Txn("deposit")
	txn.WithOpts(&TxnOpts{Value: balance})
	assert.NoError(t, err)

	err = txn.Do()
	assert.NoError(t, err)

	_, err = txn.Wait()
	assert.NoError(t, err)

	client, _ := jsonrpc.NewClient(s.HTTPAddr())
	found, err := client.Eth().GetBalance(addr, ethgo.Latest)
	assert.NoError(t, err)
	assert.Equal(t, found, balance)
}

func TestContract_EIP1559(t *testing.T) {
	s := testutil.NewTestServer(t)

	key, _ := wallet.GenerateKey()
	s.Fund(key.Address())

	cc := &testutil.Contract{}
	cc.AddOutputCaller("example")

	artifact, addr, err := s.DeployContract(cc)
	require.NoError(t, err)

	abi, err := abi.NewABIFromSlice(artifact.Abi)
	assert.NoError(t, err)

	client, _ := jsonrpc.NewClient(s.HTTPAddr())
	contract := NewContract(addr, abi, WithJsonRPC(client.Eth()), WithSender(key), WithEIP1559())

	txn, err := contract.Txn("example")
	assert.NoError(t, err)

	err = txn.Do()
	assert.NoError(t, err)

	_, err = txn.Wait()
	assert.NoError(t, err)

	// get transaction from rpc endpoint
	txnObj, err := client.Eth().GetTransactionByHash(txn.Hash())
	assert.NoError(t, err)

	assert.Zero(t, txnObj.GasPrice)
	assert.NotZero(t, txnObj.Gas)
	assert.NotZero(t, txnObj.MaxFeePerGas)
	assert.NotZero(t, txnObj.MaxPriorityFeePerGas)
}

func TestContract_CallFunctionsWithArgs(t *testing.T) {
	s := testutil.NewTestServer(t)

	cc := &testutil.Contract{}
	cc.AddCallback(func() string {
		return `
			function bar(bytes3[2] memory) public pure {}
			function baz(uint32 x, bool y) public pure returns (bool r) { r = x > 32 || y; }
			function sam(bytes memory, bool, uint[] memory) public pure {}
		`
	})

	contract, addr, err := s.DeployContract(cc)
	require.NoError(t, err)

	abi, err := abi.NewABIFromSlice(contract.Abi)
	assert.NoError(t, err)

	c := NewContract(addr, abi, WithJsonRPCEndpoint(s.HTTPAddr()))

	method := c.GetABI().GetMethod("bar")
	encoded, err := method.Encode([]interface{}{[2][3]byte{{'a', 'b', 'c'}, {'d', 'e', 'f'}}})
	assert.NoError(t, err)

	expected, err := hex.DecodeString("fce353f661626300000000000000000000000000000000000000000000000000000000006465660000000000000000000000000000000000000000000000000000000000")
	assert.NoError(t, err)
	assert.Equal(t, encoded, expected)

	method = c.GetABI().GetMethod("baz")
	encoded, err = method.Encode([]interface{}{uint32(69), true})
	assert.NoError(t, err)

	expected, err = hex.DecodeString("cdcd77c000000000000000000000000000000000000000000000000000000000000000450000000000000000000000000000000000000000000000000000000000000001")
	assert.NoError(t, err)
	assert.Equal(t, encoded, expected)

	method = c.GetABI().GetMethod("sam")
	encoded, err = method.Encode([]interface{}{[]byte("dave"), true, []uint{1, 2, 3}})
	assert.NoError(t, err)

	expected, err = hex.DecodeString("a5643bf20000000000000000000000000000000000000000000000000000000000000060000000000000000000000000000000000000000000000000000000000000000100000000000000000000000000000000000000000000000000000000000000a0000000000000000000000000000000000000000000000000000000000000000464617665000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000003000000000000000000000000000000000000000000000000000000000000000100000000000000000000000000000000000000000000000000000000000000020000000000000000000000000000000000000000000000000000000000000003")
	assert.NoError(t, err)
	assert.Equal(t, encoded, expected)
}

func TestContract_ReturnStruct(t *testing.T) {
	s := testutil.NewTestServer(t)

	cc := &testutil.Contract{}
	cc.AddCallback(func() string {
		return `
			struct S { uint32 a; uint32 b; }
			function foo() public pure returns (S memory) {
				return S(1, 2);
			}

			struct T { uint32[] a; uint32 b; }
			function bar() public pure returns (T memory) {
				uint32[] memory arr = new uint32[](3);
				arr[0] = 1;
				arr[1] = 2;
				arr[2] = 3;
				return T(arr, 4);
			}

			function baz(T memory t) public returns (T memory) { return t; }
		`
	})

	ss := map[string]interface{}{
		"a": uint32(1),
		"b": uint32(2),
	}

	tt := map[string]interface{}{
		"a": []uint32{1, 2, 3},
		"b": uint32(4),
	}

	contract, addr, err := s.DeployContract(cc)
	require.NoError(t, err)

	abi, err := abi.NewABIFromSlice(contract.Abi)
	assert.NoError(t, err)

	c := NewContract(addr, abi, WithJsonRPCEndpoint(s.HTTPAddr()))

	method := c.GetABI().GetMethod("foo")
	resp, err := c.CallInternal(method, ethgo.Latest)
	assert.NoError(t, err)

	res, err := method.Decode(resp)
	assert.NoError(t, err)
	assert.Equal(t, res["0"], ss)

	method = c.GetABI().GetMethod("bar")
	resp, err = c.CallInternal(method, ethgo.Latest)
	assert.NoError(t, err)

	res, err = method.Decode(resp)
	assert.NoError(t, err)
	assert.Equal(t, res["0"], tt)

	method = c.GetABI().GetMethod("baz")
	resp, err = c.CallInternal(method, ethgo.Latest, tt)
	assert.NoError(t, err)

	encoded, err := method.Encode([]interface{}{tt})
	assert.NoError(t, err)
	assert.Equal(t, resp, encoded[4:]) // skip the method id prefix

	res, err = method.Decode(resp)
	assert.NoError(t, err)
	assert.Equal(t, res["0"], tt)
}
