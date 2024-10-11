package contract

import (
	"bytes"
	"encoding/hex"
	"fmt"
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

	abi0, err := abi.NewABI(contract.Abi)
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

	abi, err := abi.NewABI(contract.Abi)
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

	abi, err := abi.NewABI(contract.Abi)
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

	abi, err := abi.NewABI(artifact.Abi)
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

	abi, err := abi.NewABI(artifact.Abi)
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

	abi, err := abi.NewABI(artifact.Abi)
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

func TestContract_TestEncodingABIBytes(t *testing.T) {
	s := testutil.NewTestServer(t)

	// create an address and fund it
	key, _ := wallet.GenerateKey()
	s.Fund(key.Address())

	cc := &testutil.Contract{}
	cc.AddCallback(func() string {
		return `
			struct Test1 {
				uint256 test;
			}

			struct Test2 {
				uint256[] test;
			}

			struct Test3 {
				uint256 num;
				uint256[] test;
			}
			
			Test1 test1;
			Test2 test2;
			Test3 test3;

			function setTest1() public payable {
				test1.test = 1;
			}

			function setTest2() public payable {
				test2.test.push(1);
			}

			function setTest3() public payable {
				test3.num = 1;
				test3.test.push(1);
			}

			function getTest1() public view returns (Test1 memory) {
				return test1;
			}

			function getTest2() public view returns (Test2 memory) {
				return test2;
			}

			function getTest3() public view returns (Test3 memory) {
				return test3;
			}
		`
	})

	artifact, addr, err := s.DeployContract(cc)
	require.NoError(t, err)

	abi, err := abi.NewABI(artifact.Abi)
	assert.NoError(t, err)

	contract := NewContract(addr, abi, WithJsonRPCEndpoint(s.HTTPAddr()), WithSender(key))

	set := func(id int) {
		txn, err := contract.Txn(fmt.Sprintf("setTest%d", id))
		assert.NoError(t, err)

		err = txn.Do()
		assert.NoError(t, err)

		_, err = txn.Wait()
		assert.NoError(t, err)
	}

	for i := 1; i <= 3; i++ {
		set(i)
	}

	checkVal := func(id int, expect []byte) {
		method := contract.abi.GetMethod(fmt.Sprintf("getTest%d", id))
		resp, err := contract.CallInternal(method, ethgo.Latest, id)
		assert.NoError(t, err)

		assert.Equal(t, resp, expect)
	}

	zeroBytes31 := bytes.Repeat([]byte{0}, 31)

	// tuple with only static value
	expectedValuesTest1 := append(zeroBytes31, 1) // value of num in tuple

	// tuple with only dynamic value
	expectedValuesTest2 := append(zeroBytes31, 32)                                // tuple offset because it has a dynamic value
	expectedValuesTest2 = append(expectedValuesTest2, append(zeroBytes31, 32)...) // slice offset
	expectedValuesTest2 = append(expectedValuesTest2, append(zeroBytes31, 1)...)  // slice length
	expectedValuesTest2 = append(expectedValuesTest2, append(zeroBytes31, 1)...)  // slice value

	// tuple with both static and dynamic values
	expectedValuesTest3 := append(zeroBytes31, 32)                                // tuple offset because it has a dynamic value
	expectedValuesTest3 = append(expectedValuesTest3, append(zeroBytes31, 1)...)  // value of num in tuple
	expectedValuesTest3 = append(expectedValuesTest3, append(zeroBytes31, 64)...) // slice offset
	expectedValuesTest3 = append(expectedValuesTest3, append(zeroBytes31, 1)...)  // slice length
	expectedValuesTest3 = append(expectedValuesTest3, append(zeroBytes31, 1)...)  // slice value

	checkVal(1, expectedValuesTest1)
	checkVal(2, expectedValuesTest2)
	checkVal(3, expectedValuesTest3)
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

	abi, err := abi.NewABI(artifact.Abi)
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

	abi, err := abi.NewABI(artifact.Abi)
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
