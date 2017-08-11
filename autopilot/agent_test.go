package autopilot

import (
	"bytes"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/roasbeef/btcd/btcec"
	"github.com/roasbeef/btcd/wire"
	"github.com/roasbeef/btcutil"
)

type moreChansResp struct {
	needMore bool
	amt      btcutil.Amount
}

type mockHeuristic struct {
	moreChansResps chan moreChansResp
	directiveResps chan []AttachmentDirective
}

func (m *mockHeuristic) NeedMoreChans(chans []Channel,
	balance btcutil.Amount) (btcutil.Amount, bool) {

	resp := <-m.moreChansResps
	return resp.amt, resp.needMore
}

func (m *mockHeuristic) Select(self *btcec.PublicKey, graph ChannelGraph,
	amtToUse btcutil.Amount, skipChans map[NodeID]struct{}) ([]AttachmentDirective, error) {

	resp := <-m.directiveResps
	return resp, nil
}

var _ AttachmentHeuristic = (*mockHeuristic)(nil)

type openChanIntent struct {
	target *btcec.PublicKey
	amt    btcutil.Amount
	addrs  []net.Addr
}

type mockChanController struct {
	openChanSignals chan openChanIntent
}

func (m *mockChanController) OpenChannel(target *btcec.PublicKey, amt btcutil.Amount,
	addrs []net.Addr) error {

	m.openChanSignals <- openChanIntent{
		target: target,
		amt:    amt,
		addrs:  addrs,
	}
	return nil
}

func (m *mockChanController) CloseChannel(chanPoint *wire.OutPoint) error {
	return nil
}
func (m *mockChanController) SpliceIn(chanPoint *wire.OutPoint,
	amt btcutil.Amount) (*Channel, error) {
	return nil, nil
}
func (m *mockChanController) SpliceOut(chanPoint *wire.OutPoint,
	amt btcutil.Amount) (*Channel, error) {
	return nil, nil
}

var _ ChannelController = (*mockChanController)(nil)

// TestAgentChannelOpenSignal tests that upon receipt of a chanOpenUpdate, then
// agent modifies its local state accordingly, and reconsults the heuristic.
func TestAgentChannelOpenSignal(t *testing.T) {
	t.Parallel()

	// First, we'll create all the dependencies that we'll need in order to
	// create the autopilot agent.
	self, err := randKey()
	if err != nil {
		t.Fatalf("unable to generate key: %v", err)
	}
	heuristic := &mockHeuristic{
		moreChansResps: make(chan moreChansResp),
		directiveResps: make(chan []AttachmentDirective),
	}
	chanController := &mockChanController{
		openChanSignals: make(chan openChanIntent, 10),
	}
	memGraph, _, _ := newMemChanGraph()

	// With the dependencies we created, we can now create the initial
	// agent itself.
	testCfg := Config{
		Self:           self,
		Heuristic:      heuristic,
		ChanController: chanController,
		WalletBalance: func() (btcutil.Amount, error) {
			return 0, nil
		},
		Graph: memGraph,
	}
	initialChans := []Channel{}
	agent, err := New(testCfg, initialChans)
	if err != nil {
		t.Fatalf("unable to create agent: %v", err)
	}

	// With the autopilot agent and all its dependencies we'll star the
	// primary controller goroutine.
	if err := agent.Start(); err != nil {
		t.Fatalf("unable to start agent: %v", err)
	}
	defer agent.Stop()

	var wg sync.WaitGroup

	// We'll send an initial "no" response to advance the agent past its
	// initial check.
	wg.Add(1)
	go func() {
		select {
		case heuristic.moreChansResps <- moreChansResp{false, 0}:
			wg.Done()
			return
		case <-time.After(time.Second * 10):
			t.Fatalf("heuristic wasn't queried in time")
		}
	}()

	wg.Wait()

	// Next we'll signal a new channel being opened by the backing LN node,
	// with a capacity of 1 BTC.
	newChan := Channel{
		ChanID:   randChanID(),
		Capacity: btcutil.SatoshiPerBitcoin,
	}
	agent.OnChannelOpen(newChan)

	wg = sync.WaitGroup{}

	// The agent should now query the heuristic in order to determine its
	// next action as it local state has now been modified.
	wg.Add(1)
	go func() {
		select {
		case heuristic.moreChansResps <- moreChansResp{false, 0}:
			// At this point, the local state of the agent should
			// have also been updated to reflect that the LN node
			// now has an additional channel with one BTC.
			if _, ok := agent.chanState[newChan.ChanID]; !ok {
				t.Fatalf("internal channel state wasn't updated")
			}

			// With all of our assertions passed, we'll signal the
			// main test goroutine to continue the test.
			wg.Done()
			return

		case <-time.After(time.Second * 10):
			t.Fatalf("heuristic wasn't queried in time")
		}
	}()

	// We'll wait here for either the agent to query the heuristic to be
	// queried, or for the timeout above to tick.
	wg.Wait()

	// There shouldn't be a call to the Select method as we've returned
	// "false" for NeedMoreChans above.
	select {

	// If this send success, then Select was erroneously called and the
	// test should be failed.
	case heuristic.directiveResps <- []AttachmentDirective{}:
		t.Fatalf("Select was called but shouldn't have been")

	// This is the correct path as Select should've be called.
	default:
	}
}

// TestAgentChannelCloseSignal ensures that once the agent receives an outside
// signal of a channel belonging to the backing LN node being closed, then it
// will query the heuristic to make its next decision.
func TestAgentChannelCloseSignal(t *testing.T) {
	t.Parallel()

	// First, we'll create all the dependencies that we'll need in order to
	// create the autopilot agent.
	self, err := randKey()
	if err != nil {
		t.Fatalf("unable to generate key: %v", err)
	}
	heuristic := &mockHeuristic{
		moreChansResps: make(chan moreChansResp),
		directiveResps: make(chan []AttachmentDirective),
	}
	chanController := &mockChanController{
		openChanSignals: make(chan openChanIntent),
	}
	memGraph, _, _ := newMemChanGraph()

	// With the dependencies we created, we can now create the initial
	// agent itself.
	testCfg := Config{
		Self:           self,
		Heuristic:      heuristic,
		ChanController: chanController,
		WalletBalance: func() (btcutil.Amount, error) {
			return 0, nil
		},
		Graph: memGraph,
	}

	// We'll start the agent with two channels already being active.
	initialChans := []Channel{
		{
			ChanID:   randChanID(),
			Capacity: btcutil.SatoshiPerBitcoin,
		},
		{
			ChanID:   randChanID(),
			Capacity: btcutil.SatoshiPerBitcoin * 2,
		},
	}
	agent, err := New(testCfg, initialChans)
	if err != nil {
		t.Fatalf("unable to create agent: %v", err)
	}

	// With the autopilot agent and all its dependencies we'll star the
	// primary controller goroutine.
	if err := agent.Start(); err != nil {
		t.Fatalf("unable to start agent: %v", err)
	}
	defer agent.Stop()

	var wg sync.WaitGroup

	// We'll send an initial "no" response to advance the agent past its
	// initial check.
	wg.Add(1)
	go func() {
		select {
		case heuristic.moreChansResps <- moreChansResp{false, 0}:
			wg.Done()
			return
		case <-time.After(time.Second * 10):
			t.Fatalf("heuristic wasn't queried in time")
		}
	}()

	wg.Wait()

	// Next, we'll close both channels which should force the agent to
	// re-query the heuristic.
	agent.OnChannelClose(initialChans[0].ChanID, initialChans[1].ChanID)

	wg = sync.WaitGroup{}

	// The agent should now query the heuristic in order to determine its
	// next action as it local state has now been modified.
	wg.Add(1)
	go func() {
		select {
		case heuristic.moreChansResps <- moreChansResp{false, 0}:
			// At this point, the local state of the agent should
			// have also been updated to reflect that the LN node
			// has no existing open channels.
			if len(agent.chanState) != 0 {
				t.Fatalf("internal channel state wasn't updated")
			}

			// With all of our assertions passed, we'll signal the
			// main test goroutine to continue the test.
			wg.Done()
			return

		case <-time.After(time.Second * 10):
			t.Fatalf("heuristic wasn't queried in time")
		}
	}()

	// We'll wait here for either the agent to query the heuristic to be
	// queried, or for the timeout above to tick.
	wg.Wait()

	// There shouldn't be a call to the Select method as we've returned
	// "false" for NeedMoreChans above.
	select {

	// If this send success, then Select was erroneously called and the
	// test should be failed.
	case heuristic.directiveResps <- []AttachmentDirective{}:
		t.Fatalf("Select was called but shouldn't have been")

	// This is the correct path as Select should've be called.
	default:
	}
}

// TestAgentBalanceUpdateIncrease ensures that once the agent receives an
// outside signal concerning a balance update, then it will re-query the
// heuristic to determine its next action.
func TestAgentBalanceUpdate(t *testing.T) {
	t.Parallel()

	// First, we'll create all the dependencies that we'll need in order to
	// create the autopilot agent.
	self, err := randKey()
	if err != nil {
		t.Fatalf("unable to generate key: %v", err)
	}
	heuristic := &mockHeuristic{
		moreChansResps: make(chan moreChansResp),
		directiveResps: make(chan []AttachmentDirective),
	}
	chanController := &mockChanController{
		openChanSignals: make(chan openChanIntent),
	}
	memGraph, _, _ := newMemChanGraph()

	// The wallet will start with 2 BTC available.
	const walletBalance = btcutil.SatoshiPerBitcoin * 2

	// With the dependencies we created, we can now create the initial
	// agent itself.
	testCfg := Config{
		Self:           self,
		Heuristic:      heuristic,
		ChanController: chanController,
		WalletBalance: func() (btcutil.Amount, error) {
			return walletBalance, nil
		},
		Graph: memGraph,
	}
	initialChans := []Channel{}
	agent, err := New(testCfg, initialChans)
	if err != nil {
		t.Fatalf("unable to create agent: %v", err)
	}

	// With the autopilot agent and all its dependencies we'll star the
	// primary controller goroutine.
	if err := agent.Start(); err != nil {
		t.Fatalf("unable to start agent: %v", err)
	}
	defer agent.Stop()

	var wg sync.WaitGroup

	// We'll send an initial "no" response to advance the agent past its
	// initial check.
	wg.Add(1)
	go func() {
		select {
		case heuristic.moreChansResps <- moreChansResp{false, 0}:
			wg.Done()
			return
		case <-time.After(time.Second * 10):
			t.Fatalf("heuristic wasn't queried in time")
		}
	}()

	wg.Wait()

	// Next we'll send a new balance update signal to the agent, adding 5
	// BTC to the amount of available funds.
	const balanceDelta = btcutil.SatoshiPerBitcoin * 5
	agent.OnBalanceChange(balanceDelta)

	wg = sync.WaitGroup{}

	// The agent should now query the heuristic in order to determine its
	// next action as it local state has now been modified.
	wg.Add(1)
	go func() {
		select {
		case heuristic.moreChansResps <- moreChansResp{false, 0}:
			// At this point, the local state of the agent should
			// have also been updated to reflect that the LN node
			// now has an additional  5BTC available.
			const expectedAmt = walletBalance + balanceDelta
			if agent.totalBalance != expectedAmt {
				t.Fatalf("expected %v wallet balance "+
					"instead have %v", agent.totalBalance,
					expectedAmt)
			}

			// With all of our assertions passed, we'll signal the
			// main test goroutine to continue the test.
			wg.Done()
			return

		case <-time.After(time.Second * 10):
			t.Fatalf("heuristic wasn't queried in time")
		}
	}()

	// We'll wait here for either the agent to query the heuristic to be
	// queried, or for the timeout above to tick.
	wg.Wait()

	// There shouldn't be a call to the Select method as we've returned
	// "false" for NeedMoreChans above.
	select {

	// If this send success, then Select was erroneously called and the
	// test should be failed.
	case heuristic.directiveResps <- []AttachmentDirective{}:
		t.Fatalf("Select was called but shouldn't have been")

	// This is the correct path as Select should've be called.
	default:
	}
}

// TestAgentImmediateAttach tests that if an autopilot agent is created, and it
// has enough funds available to create channels, then it does so immediately.
func TestAgentImmediateAttach(t *testing.T) {
	t.Parallel()

	// First, we'll create all the dependencies that we'll need in order to
	// create the autopilot agent.
	self, err := randKey()
	if err != nil {
		t.Fatalf("unable to generate key: %v", err)
	}
	heuristic := &mockHeuristic{
		moreChansResps: make(chan moreChansResp),
		directiveResps: make(chan []AttachmentDirective),
	}
	chanController := &mockChanController{
		openChanSignals: make(chan openChanIntent),
	}
	memGraph, _, _ := newMemChanGraph()

	// The wallet will start with 10 BTC available.
	const walletBalance = btcutil.SatoshiPerBitcoin * 10

	// With the dependencies we created, we can now create the initial
	// agent itself.
	testCfg := Config{
		Self:           self,
		Heuristic:      heuristic,
		ChanController: chanController,
		WalletBalance: func() (btcutil.Amount, error) {
			return walletBalance, nil
		},
		Graph: memGraph,
	}
	initialChans := []Channel{}
	agent, err := New(testCfg, initialChans)
	if err != nil {
		t.Fatalf("unable to create agent: %v", err)
	}

	// With the autopilot agent and all its dependencies we'll star the
	// primary controller goroutine.
	if err := agent.Start(); err != nil {
		t.Fatalf("unable to start agent: %v", err)
	}
	defer agent.Stop()

	var wg sync.WaitGroup

	// The very first thing the agent should do is query the NeedMoreChans
	// method on the passed heuristic. So we'll provide it with a response
	// that will kick off the main loop.
	wg.Add(1)
	go func() {
		select {

		// We'll send over a response indicating that it should
		// establish more channels, and give it a budget of 5 BTC to do
		// so.
		case heuristic.moreChansResps <- moreChansResp{true, 5 * btcutil.SatoshiPerBitcoin}:
			wg.Done()
			return
		case <-time.After(time.Second * 10):
			t.Fatalf("heuristic wasn't queried in time")
		}
	}()

	// We'll wait here for the agent to query the heuristic. If ti doesn't
	// do so within 10 seconds, then the test will fail out.
	wg.Wait()

	// At this point, the agent should now be querying the heuristic to
	// requests attachment directives. We'll generate 5 mock directives so
	// it can progress within its loop.
	const numChans = 5
	directives := make([]AttachmentDirective, numChans)
	for i := 0; i < numChans; i++ {
		directives[i] = AttachmentDirective{
			PeerKey: self,
			ChanAmt: btcutil.SatoshiPerBitcoin,
			Addrs: []net.Addr{
				&net.TCPAddr{
					IP: bytes.Repeat([]byte("a"), 16),
				},
			},
		}
	}

	wg = sync.WaitGroup{}

	// With our fake directives created, we'll now send then to the agent
	// as a return value for the Select function.
	wg.Add(1)
	go func() {
		select {
		case heuristic.directiveResps <- directives:
			wg.Done()
			return
		case <-time.After(time.Second * 10):
			t.Fatalf("heuristic wasn't queried in time")
		}
	}()

	// We'll wait here for either the agent to query the heuristic to be
	// queried, or for the timeout above to tick.
	wg.Wait()

	// Finally, we should receive 5 calls to the OpenChannel method with
	// the exact same parameters that we specified within the attachment
	// directives.
	for i := 0; i < numChans; i++ {
		select {
		case openChan := <-chanController.openChanSignals:
			if openChan.amt != btcutil.SatoshiPerBitcoin {
				t.Fatalf("invalid chan amt: expected %v, got %v",
					btcutil.SatoshiPerBitcoin, openChan.amt)
			}
			if !openChan.target.IsEqual(self) {
				t.Fatalf("unexpected key: expected %x, got %x",
					self.SerializeCompressed(),
					openChan.target.SerializeCompressed())
			}
			if len(openChan.addrs) != 1 {
				t.Fatalf("should have single addr, instead have: %v",
					len(openChan.addrs))
			}
		case <-time.After(time.Second * 10):
			t.Fatalf("channel not opened in time")
		}
	}
}
