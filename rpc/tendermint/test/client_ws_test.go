// +build integration

// Space above here matters
package test

import (
	"fmt"
	"testing"

	"github.com/eris-ltd/eris-db/txs"
	"github.com/stretchr/testify/assert"
	_ "github.com/tendermint/tendermint/config/tendermint_test"
)

var wsTyp = "JSONRPC"

//--------------------------------------------------------------------------------
// Test the websocket service

// make a simple connection to the server
func TestWSConnect(t *testing.T) {
	wsc := newWSClient(t)
	wsc.Stop()
}

// receive a new block message
func TestWSNewBlock(t *testing.T) {
	wsc := newWSClient(t)
	eid := txs.EventStringNewBlock()
	subId := subscribeAndGetSubscriptionId(t, wsc, eid)
	defer func() {
		unsubscribe(t, wsc, subId)
		wsc.Stop()
	}()
	waitForEvent(t, wsc, eid, func() {},
		func(eid string, eventData txs.EventData) (bool, error) {
			fmt.Println("Check: ", eventData.(txs.EventDataNewBlock).Block)
			return true, nil
		})
}

// receive a few new block messages in a row, with increasing height
func TestWSBlockchainGrowth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}
	wsc := newWSClient(t)
	eid := txs.EventStringNewBlock()
	subId := subscribeAndGetSubscriptionId(t, wsc, eid)
	defer func() {
		unsubscribe(t, wsc, subId)
		wsc.Stop()
	}()
	// listen for NewBlock, ensure height increases by 1
	var initBlockN int
	for i := 0; i < 2; i++ {
		waitForEvent(t, wsc, eid, func() {},
			func(eid string, eventData txs.EventData) (bool, error) {
				eventDataNewBlock, ok := eventData.(txs.EventDataNewBlock)
				if !ok {
					t.Fatalf("Was expecting EventDataNewBlock but got %v", eventData)
				}
				block := eventDataNewBlock.Block
				if i == 0 {
					initBlockN = block.Height
				} else {
					if block.Header.Height != initBlockN+i {
						return true, fmt.Errorf("Expected block %d, got block %d", i,
							block.Header.Height)
					}
				}

				return true, nil
			})
	}
}

// send a transaction and validate the events from listening for both sender and receiver
func TestWSSend(t *testing.T) {
	toAddr := user[1].Address
	amt := int64(100)

	wsc := newWSClient(t)
	eidInput := txs.EventStringAccInput(user[0].Address)
	eidOutput := txs.EventStringAccOutput(toAddr)
	subIdInput := subscribeAndGetSubscriptionId(t, wsc, eidInput)
	subIdOutput := subscribeAndGetSubscriptionId(t, wsc, eidOutput)
	defer func() {
		unsubscribe(t, wsc, subIdInput)
		unsubscribe(t, wsc, subIdOutput)
		wsc.Stop()
	}()
	waitForEvent(t, wsc, eidInput, func() {
		tx := makeDefaultSendTxSigned(t, wsTyp, toAddr, amt)
		broadcastTx(t, wsTyp, tx)
	}, unmarshalValidateSend(amt, toAddr))

	waitForEvent(t, wsc, eidOutput, func() {},
		unmarshalValidateSend(amt, toAddr))
}

// ensure events are only fired once for a given transaction
func TestWSDoubleFire(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}
	wsc := newWSClient(t)
	eid := txs.EventStringAccInput(user[0].Address)
	subId := subscribeAndGetSubscriptionId(t, wsc, eid)
	defer func() {
		unsubscribe(t, wsc, subId)
		wsc.Stop()
	}()
	amt := int64(100)
	toAddr := user[1].Address
	// broadcast the transaction, wait to hear about it
	waitForEvent(t, wsc, eid, func() {
		tx := makeDefaultSendTxSigned(t, wsTyp, toAddr, amt)
		broadcastTx(t, wsTyp, tx)
	}, func(eid string, b txs.EventData) (bool, error) {
		return true, nil
	})
	// but make sure we don't hear about it twice
	err := waitForEvent(t, wsc, eid,
		func() {},
		func(eid string, b txs.EventData) (bool, error) {
			return false, nil
		})
	assert.True(t, err.Timeout(), "We should have timed out waiting for second"+
		" %v event", eid)
}

// create a contract, wait for the event, and send it a msg, validate the return
func TestWSCallWait(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}
	wsc := newWSClient(t)
	eid1 := txs.EventStringAccInput(user[0].Address)
	subId1 := subscribeAndGetSubscriptionId(t, wsc, eid1)
	defer func() {
		unsubscribe(t, wsc, subId1)
		wsc.Stop()
	}()
	amt, gasLim, fee := int64(10000), int64(1000), int64(1000)
	code, returnCode, returnVal := simpleContract()
	var contractAddr []byte
	// wait for the contract to be created
	waitForEvent(t, wsc, eid1, func() {
		tx := makeDefaultCallTx(t, wsTyp, nil, code, amt, gasLim, fee)
		receipt := broadcastTx(t, wsTyp, tx)
		contractAddr = receipt.ContractAddr
	}, unmarshalValidateTx(amt, returnCode))

	// susbscribe to the new contract
	amt = int64(10001)
	eid2 := txs.EventStringAccOutput(contractAddr)
	subId2 := subscribeAndGetSubscriptionId(t, wsc, eid2)
	defer func() {
		unsubscribe(t, wsc, subId2)
	}()
	// get the return value from a call
	data := []byte{0x1}
	waitForEvent(t, wsc, eid2, func() {
		tx := makeDefaultCallTx(t, wsTyp, contractAddr, data, amt, gasLim, fee)
		receipt := broadcastTx(t, wsTyp, tx)
		contractAddr = receipt.ContractAddr
	}, unmarshalValidateTx(amt, returnVal))
}

// create a contract and send it a msg without waiting. wait for contract event
// and validate return
func TestWSCallNoWait(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}
	wsc := newWSClient(t)
	amt, gasLim, fee := int64(10000), int64(1000), int64(1000)
	code, _, returnVal := simpleContract()

	tx := makeDefaultCallTx(t, wsTyp, nil, code, amt, gasLim, fee)
	receipt := broadcastTx(t, wsTyp, tx)
	contractAddr := receipt.ContractAddr

	// susbscribe to the new contract
	amt = int64(10001)
	eid := txs.EventStringAccOutput(contractAddr)
	subId := subscribeAndGetSubscriptionId(t, wsc, eid)
	defer func() {
		unsubscribe(t, wsc, subId)
		wsc.Stop()
	}()
	// get the return value from a call
	data := []byte{0x1}
	waitForEvent(t, wsc, eid, func() {
		tx := makeDefaultCallTx(t, wsTyp, contractAddr, data, amt, gasLim, fee)
		broadcastTx(t, wsTyp, tx)
	}, unmarshalValidateTx(amt, returnVal))
}

// create two contracts, one of which calls the other
func TestWSCallCall(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}
	wsc := newWSClient(t)
	amt, gasLim, fee := int64(10000), int64(1000), int64(1000)
	code, _, returnVal := simpleContract()
	txid := new([]byte)

	// deploy the two contracts
	tx := makeDefaultCallTx(t, wsTyp, nil, code, amt, gasLim, fee)
	receipt := broadcastTx(t, wsTyp, tx)
	contractAddr1 := receipt.ContractAddr

	code, _, _ = simpleCallContract(contractAddr1)
	tx = makeDefaultCallTx(t, wsTyp, nil, code, amt, gasLim, fee)
	receipt = broadcastTx(t, wsTyp, tx)
	contractAddr2 := receipt.ContractAddr

	// subscribe to the new contracts
	amt = int64(10001)
	eid := txs.EventStringAccCall(contractAddr1)
	subId := subscribeAndGetSubscriptionId(t, wsc, eid)
	defer func() {
		unsubscribe(t, wsc, subId)
		wsc.Stop()
	}()
	// call contract2, which should call contract1, and wait for ev1

	// let the contract get created first
	waitForEvent(t, wsc, eid, func() {
	}, func(eid string, b txs.EventData) (bool, error) {
		return true, nil
	})
	// call it
	waitForEvent(t, wsc, eid, func() {
		tx := makeDefaultCallTx(t, wsTyp, contractAddr2, nil, amt, gasLim, fee)
		broadcastTx(t, wsTyp, tx)
		*txid = txs.TxHash(chainID, tx)
	}, unmarshalValidateCall(user[0].Address, returnVal, txid))
}

func TestSubscribe(t *testing.T) {
	testSubscribe(t)
}
