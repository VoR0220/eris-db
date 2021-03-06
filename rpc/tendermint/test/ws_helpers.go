package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	ctypes "github.com/eris-ltd/eris-db/rpc/tendermint/core/types"
	"github.com/eris-ltd/eris-db/txs"
	"github.com/tendermint/tendermint/types"
	tm_types "github.com/tendermint/tendermint/types"

	edbcli "github.com/eris-ltd/eris-db/rpc/tendermint/client"
	"github.com/tendermint/go-events"
	client "github.com/tendermint/go-rpc/client"
	rpctypes "github.com/tendermint/go-rpc/types"
	"github.com/tendermint/go-wire"
)

const (
	timeoutSeconds = 2
)

//--------------------------------------------------------------------------------
// Utilities for testing the websocket service

// create a new connection
func newWSClient(t *testing.T) *client.WSClient {
	wsc := client.NewWSClient(websocketAddr, websocketEndpoint)
	if _, err := wsc.Start(); err != nil {
		t.Fatal(err)
	}
	return wsc
}

// subscribe to an event
func subscribe(t *testing.T, wsc *client.WSClient, eventId string) {
	if err := wsc.Subscribe(eventId); err != nil {
		t.Fatal(err)
	}
}

func subscribeAndGetSubscriptionId(t *testing.T, wsc *client.WSClient,
	eventId string) string {
	if err := wsc.Subscribe(eventId); err != nil {
		t.Fatal(err)
	}

	timeout := time.NewTimer(timeoutSeconds * time.Second)
	for {
		select {
		case <-timeout.C:
			t.Fatal("Timeout waiting for subscription result")
		case bs := <-wsc.ResultsCh:
			resultSubscribe, ok := readResult(t, bs).(*ctypes.ResultSubscribe)
			if ok {
				return resultSubscribe.SubscriptionId
			}
		}
	}
}

// unsubscribe from an event
func unsubscribe(t *testing.T, wsc *client.WSClient, subscriptionId string) {
	if err := wsc.Unsubscribe(subscriptionId); err != nil {
		t.Fatal(err)
	}
}

// broadcast transaction and wait for new block
func broadcastTxAndWaitForBlock(t *testing.T, typ string, wsc *client.WSClient,
	tx txs.Tx) (txs.Receipt, error) {
	var rec txs.Receipt
	var err error
	initialHeight := -1
	runThenWaitForBlock(t, wsc,
		func(block *tm_types.Block) bool {
			if initialHeight < 0 {
				initialHeight = block.Height
				return false
			} else {
				return block.Height > initialHeight
			}
		},
		func() {
			rec, err = edbcli.BroadcastTx(clients[typ], tx)
			mempoolCount += 1
		})
	return rec, err
}

func waitNBlocks(t *testing.T, wsc *client.WSClient, n int) {
	i := 0
	runThenWaitForBlock(t, wsc,
		func(block *tm_types.Block) bool {
			i++
			return i <= n
		},
		func() {})
}

func runThenWaitForBlock(t *testing.T, wsc *client.WSClient,
	blockPredicate func(*tm_types.Block) bool, runner func()) {
	subscribeAndWaitForNext(t, wsc, txs.EventStringNewBlock(),
		runner,
		func(event string, eventData txs.EventData) (bool, error) {
			return blockPredicate(eventData.(txs.EventDataNewBlock).Block), nil
		})
}

func subscribeAndWaitForNext(t *testing.T, wsc *client.WSClient, event string,
	runner func(),
	eventDataChecker func(string, txs.EventData) (bool, error)) {
	subId := subscribeAndGetSubscriptionId(t, wsc, event)
	defer unsubscribe(t, wsc, subId)
	waitForEvent(t,
		wsc,
		event,
		runner,
		eventDataChecker)
}

// waitForEvent executes runner that is expected to trigger events. It then
// waits for any events on the supplies WSClient and checks the eventData with
// the eventDataChecker which is a function that is passed the event name
// and the EventData and returns the pair of stopWaiting, err. Where if
// stopWaiting is true waitForEvent will return or if stopWaiting is false
// waitForEvent will keep listening for new events. If an error is returned
// waitForEvent will fail the test.
func waitForEvent(t *testing.T, wsc *client.WSClient, eventid string,
	runner func(),
	eventDataChecker func(string, txs.EventData) (bool, error)) waitForEventError {

	// go routine to wait for websocket msg
	goodCh := make(chan txs.EventData)
	errCh := make(chan error)

	// do stuff (transactions)
	runner()

	// Read message
	go func() {
		var err error
	LOOP:
		for {
			select {
			case r := <-wsc.ResultsCh:
				result := new(ctypes.ErisDBResult)
				wire.ReadJSONPtr(result, r, &err)
				if err != nil {
					errCh <- err
					break LOOP
				}
				event, ok := (*result).(*ctypes.ResultEvent)
				if ok && event.Event == eventid {
					goodCh <- event.Data
					break LOOP
				}
			case err := <-wsc.ErrorsCh:
				errCh <- err
				break LOOP
			case <-wsc.Quit:
				break LOOP
			}
		}
	}()

	// wait for an event or timeout
	timeout := time.NewTimer(timeoutSeconds * time.Second)
	for {
		select {
		case <-timeout.C:
			return waitForEventError{timeout: true}
		case eventData := <-goodCh:
			// run the check
			stopWaiting, err := eventDataChecker(eventid, eventData)
			if err != nil {
				t.Fatal(err) // Show the stack trace.
			}
			if stopWaiting {
				return waitForEventError{}
			}
		case err := <-errCh:
			t.Fatal(err)
		}
	}
}

type waitForEventError struct {
	error
	timeout bool
}

func (err waitForEventError) Timeout() bool {
	return err.timeout
}

func acceptFirstBlock(_ *tm_types.Block) bool {
	return true
}

//--------------------------------------------------------------------------------

func unmarshalResponseNewBlock(b []byte) (*types.Block, error) {
	// unmarshall and assert somethings
	var response rpctypes.RPCResponse
	var err error
	wire.ReadJSON(&response, b, &err)
	if err != nil {
		return nil, err
	}
	if response.Error != "" {
		return nil, fmt.Errorf(response.Error)
	}
	// TODO
	//block := response.Result.(*ctypes.ResultEvent).Data.(types.EventDataNewBlock).Block
	// return block, nil
	return nil, nil
}

func unmarshalResponseNameReg(b []byte) (*txs.NameTx, error) {
	// unmarshall and assert somethings
	var response rpctypes.RPCResponse
	var err error
	wire.ReadJSON(&response, b, &err)
	if err != nil {
		return nil, err
	}
	if response.Error != "" {
		return nil, fmt.Errorf(response.Error)
	}
	_, val := UnmarshalEvent(*response.Result)
	tx := txs.DecodeTx(val.(types.EventDataTx).Tx).(*txs.NameTx)
	return tx, nil
}

func unmarshalValidateSend(amt int64,
	toAddr []byte) func(string, txs.EventData) (bool, error) {
	return func(eid string, eventData txs.EventData) (bool, error) {
		var data = eventData.(txs.EventDataTx)
		if data.Exception != "" {
			return true, fmt.Errorf(data.Exception)
		}
		tx := data.Tx.(*txs.SendTx)
		if !bytes.Equal(tx.Inputs[0].Address, user[0].Address) {
			return true, fmt.Errorf("Senders do not match up! Got %x, expected %x", tx.Inputs[0].Address, user[0].Address)
		}
		if tx.Inputs[0].Amount != amt {
			return true, fmt.Errorf("Amt does not match up! Got %d, expected %d", tx.Inputs[0].Amount, amt)
		}
		if !bytes.Equal(tx.Outputs[0].Address, toAddr) {
			return true, fmt.Errorf("Receivers do not match up! Got %x, expected %x", tx.Outputs[0].Address, user[0].Address)
		}
		return true, nil
	}
}

func unmarshalValidateTx(amt int64,
	returnCode []byte) func(string, txs.EventData) (bool, error) {
	return func(eid string, eventData txs.EventData) (bool, error) {
		var data = eventData.(txs.EventDataTx)
		if data.Exception != "" {
			return true, fmt.Errorf(data.Exception)
		}
		tx := data.Tx.(*txs.CallTx)
		if !bytes.Equal(tx.Input.Address, user[0].Address) {
			return true, fmt.Errorf("Senders do not match up! Got %x, expected %x",
				tx.Input.Address, user[0].Address)
		}
		if tx.Input.Amount != amt {
			return true, fmt.Errorf("Amt does not match up! Got %d, expected %d",
				tx.Input.Amount, amt)
		}
		ret := data.Return
		if !bytes.Equal(ret, returnCode) {
			return true, fmt.Errorf("Tx did not return correctly. Got %x, expected %x", ret, returnCode)
		}
		return true, nil
	}
}

func unmarshalValidateCall(origin,
	returnCode []byte, txid *[]byte) func(string, txs.EventData) (bool, error) {
	return func(eid string, eventData txs.EventData) (bool, error) {
		var data = eventData.(txs.EventDataCall)
		if data.Exception != "" {
			return true, fmt.Errorf(data.Exception)
		}
		if !bytes.Equal(data.Origin, origin) {
			return true, fmt.Errorf("Origin does not match up! Got %x, expected %x",
				data.Origin, origin)
		}
		ret := data.Return
		if !bytes.Equal(ret, returnCode) {
			return true, fmt.Errorf("Call did not return correctly. Got %x, expected %x", ret, returnCode)
		}
		if !bytes.Equal(data.TxID, *txid) {
			return true, fmt.Errorf("TxIDs do not match up! Got %x, expected %x",
				data.TxID, *txid)
		}
		return true, nil
	}
}

// Unmarshal a json event
func UnmarshalEvent(b json.RawMessage) (string, events.EventData) {
	var err error
	result := new(ctypes.ErisDBResult)
	wire.ReadJSONPtr(result, b, &err)
	if err != nil {
		panic(err)
	}
	event, ok := (*result).(*ctypes.ResultEvent)
	if !ok {
		return "", nil // TODO: handle non-event messages (ie. return from subscribe/unsubscribe)
		// fmt.Errorf("Result is not type *ctypes.ResultEvent. Got %v", reflect.TypeOf(*result))
	}
	return event.Event, event.Data
}

func readResult(t *testing.T, bs []byte) ctypes.ErisDBResult {
	var err error
	result := new(ctypes.ErisDBResult)
	wire.ReadJSONPtr(result, bs, &err)
	if err != nil {
		t.Fatal(err)
	}
	return *result
}
