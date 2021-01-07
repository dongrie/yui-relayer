package core

import (
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	clienttypes "github.com/cosmos/cosmos-sdk/x/ibc/core/02-client/types"
	"github.com/gogo/protobuf/proto"
)

type ChainI interface {
	ClientType() string
	ChainID() string
	ClientID() string
	GetAddress() (sdk.AccAddress, error)

	SetPath(p *PathEnd) error

	QueryLatestHeader() (out HeaderI, err error)
	// height represents the height of src chain
	QueryClientState(height int64) (*clienttypes.QueryClientStateResponse, error)

	// Is first return value needed?
	SendMsgs(msgs []sdk.Msg) ([]byte, error)
	// Send sends msgs to the chain and logging a result of it
	// It returns a boolean value whether the result is success
	Send(msgs []sdk.Msg) bool

	Update(key, value string) (ChainConfigI, error)

	// MakeMsgCreateClient creates a CreateClientMsg to this chain
	MakeMsgCreateClient(clientID string, dstHeader HeaderI, signer sdk.AccAddress) (sdk.Msg, error)

	StartEventListener(dst ChainI, strategy StrategyI)

	Init(homePath string, timeout time.Duration, debug bool) error
}

type ChainConfigI interface {
	proto.Message
	GetChain() ChainI
}