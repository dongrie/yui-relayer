package core

import (
	"fmt"
	"time"

	retry "github.com/avast/retry-go"
	chantypes "github.com/cosmos/ibc-go/v7/modules/core/04-channel/types"
	"github.com/hyperledger-labs/yui-relayer/logger"
)

// CreateChannel runs the channel creation messages on timeout until they pass
// TODO: add max retries or something to this function
func CreateChannel(src, dst *ProvableChain, ordered bool, to time.Duration) error {
	relayLogger := logger.GetLogger()
	var order chantypes.Order
	if ordered {
		order = chantypes.ORDERED
	} else {
		order = chantypes.UNORDERED
	}

	ticker := time.NewTicker(to)
	failures := 0
	for ; true; <-ticker.C {
		chanSteps, err := createChannelStep(src, dst, order)
		if err != nil {
			GetChannelLoggerFromProvaleChain(relayLogger, src, dst).Error(
				"failed to create channel step",
				err,
			)
			return err
		}

		if !chanSteps.Ready() {
			break
		}

		chanSteps.Send(src, dst)

		switch {
		// In the case of success and this being the last transaction
		// debug logging, log created connection and break
		case chanSteps.Success() && chanSteps.Last:
			GetChannelLoggerFromProvaleChain(relayLogger, src, dst).Info(
				"★ Channel created",
			)
			return nil
		// In the case of success, reset the failures counter
		case chanSteps.Success():
			failures = 0
			continue
		// In the case of failure, increment the failures counter and exit if this is the 3rd failure
		case !chanSteps.Success():
			failures++
			relayLogger.Info("retrying transaction...")
			time.Sleep(5 * time.Second)
			if failures > 2 {
				GetChannelLoggerFromProvaleChain(relayLogger, src, dst).Error(
					"! Channel failed",
					err,
				)
				return fmt.Errorf("! Channel failed: [%s]chan{%s}port{%s} -> [%s]chan{%s}port{%s}",
					src.ChainID(), src.Path().ClientID, src.Path().ChannelID,
					dst.ChainID(), dst.Path().ClientID, dst.Path().ChannelID,
				)
			}
		}
	}

	return nil
}

func createChannelStep(src, dst *ProvableChain, ordering chantypes.Order) (*RelayMsgs, error) {
	out := NewRelayMsgs()
	if err := validatePaths(src, dst); err != nil {
		return nil, err
	}
	// First, update the light clients to the latest header and return the header
	sh, err := NewSyncHeaders(src, dst)
	if err != nil {
		return nil, err
	}

	// Query a number of things all at once
	var (
		srcUpdateHeaders, dstUpdateHeaders []Header
	)

	err = retry.Do(func() error {
		srcUpdateHeaders, dstUpdateHeaders, err = sh.SetupBothHeadersForUpdate(src, dst)
		return err
	}, rtyAtt, rtyDel, rtyErr, retry.OnRetry(func(n uint, err error) {
		// logRetryUpdateHeaders(src, dst, n, err)
		if err := sh.Updates(src, dst); err != nil {
			panic(err)
		}
	}))
	if err != nil {
		return nil, err
	}

	srcChan, dstChan, err := QueryChannelPair(sh.GetQueryContext(src.ChainID()), sh.GetQueryContext(dst.ChainID()), src, dst)
	if err != nil {
		return nil, err
	}

	switch {
	// Handshake hasn't been started on src or dst, relay `chanOpenInit` to src
	case srcChan.Channel.State == chantypes.UNINITIALIZED && dstChan.Channel.State == chantypes.UNINITIALIZED:
		logChannelStates(src, dst, srcChan, dstChan)
		addr := mustGetAddress(src)
		out.Src = append(out.Src,
			src.Path().ChanInit(dst.Path(), addr),
		)
	// Handshake has started on dst (1 step done), relay `chanOpenTry` and `updateClient` to src
	case srcChan.Channel.State == chantypes.UNINITIALIZED && dstChan.Channel.State == chantypes.INIT:
		logChannelStates(src, dst, srcChan, dstChan)
		addr := mustGetAddress(src)
		if len(dstUpdateHeaders) > 0 {
			out.Src = append(out.Src, src.Path().UpdateClients(dstUpdateHeaders, addr)...)
		}
		out.Src = append(out.Src, src.Path().ChanTry(dst.Path(), dstChan, addr))
	// Handshake has started on src (1 step done), relay `chanOpenTry` and `updateClient` to dst
	case srcChan.Channel.State == chantypes.INIT && dstChan.Channel.State == chantypes.UNINITIALIZED:
		logChannelStates(dst, src, dstChan, srcChan)
		addr := mustGetAddress(dst)
		if len(srcUpdateHeaders) > 0 {
			out.Dst = append(out.Dst, dst.Path().UpdateClients(srcUpdateHeaders, addr)...)
		}
		out.Dst = append(out.Dst, dst.Path().ChanTry(src.Path(), srcChan, addr))

	// Handshake has started on src (2 steps done), relay `chanOpenAck` and `updateClient` to dst
	case srcChan.Channel.State == chantypes.TRYOPEN && dstChan.Channel.State == chantypes.INIT:
		logChannelStates(dst, src, dstChan, srcChan)
		addr := mustGetAddress(dst)
		if len(srcUpdateHeaders) > 0 {
			out.Dst = append(out.Dst, dst.Path().UpdateClients(srcUpdateHeaders, addr)...)
		}
		out.Dst = append(out.Dst, dst.Path().ChanAck(src.Path(), srcChan, addr))

	// Handshake has started on dst (2 steps done), relay `chanOpenAck` and `updateClient` to src
	case srcChan.Channel.State == chantypes.INIT && dstChan.Channel.State == chantypes.TRYOPEN:
		logChannelStates(src, dst, srcChan, dstChan)
		addr := mustGetAddress(src)
		if len(dstUpdateHeaders) > 0 {
			out.Src = append(out.Src, src.Path().UpdateClients(dstUpdateHeaders, addr)...)
		}
		out.Src = append(out.Src, src.Path().ChanAck(dst.Path(), dstChan, addr))

	// Handshake has confirmed on dst (3 steps done), relay `chanOpenConfirm` and `updateClient` to src
	case srcChan.Channel.State == chantypes.TRYOPEN && dstChan.Channel.State == chantypes.OPEN:
		logChannelStates(src, dst, srcChan, dstChan)
		addr := mustGetAddress(src)
		if len(dstUpdateHeaders) > 0 {
			out.Src = append(out.Src, src.Path().UpdateClients(dstUpdateHeaders, addr)...)
		}
		out.Src = append(out.Src, src.Path().ChanConfirm(dstChan, addr))
		out.Last = true

	// Handshake has confirmed on src (3 steps done), relay `chanOpenConfirm` and `updateClient` to dst
	case srcChan.Channel.State == chantypes.OPEN && dstChan.Channel.State == chantypes.TRYOPEN:
		logChannelStates(dst, src, dstChan, srcChan)
		addr := mustGetAddress(dst)
		if len(srcUpdateHeaders) > 0 {
			out.Dst = append(out.Dst, dst.Path().UpdateClients(srcUpdateHeaders, addr)...)
		}
		out.Dst = append(out.Dst, dst.Path().ChanConfirm(srcChan, addr))
		out.Last = true
	default:
		panic(fmt.Sprintf("not implemeneted error: %v <=> %v", srcChan.Channel.State.String(), dstChan.Channel.State.String()))
	}
	return out, nil
}

func logChannelStates(src, dst *ProvableChain, srcChan, dstChan *chantypes.QueryChannelResponse) {
	relayLogger := logger.GetLogger()
	// sLogger :=
	GetChannelLoggerFromProvaleChain(relayLogger, src, dst).Info(
		"channel states",
		"msg",
		fmt.Sprintf(
			"src channel height: [%d] state: %s | dst channel height: [%d] state: %s",
			mustGetHeight(srcChan.ProofHeight),
			srcChan.Channel.State,
			mustGetHeight(dstChan.ProofHeight),
			dstChan.Channel.State,
		),
	)
}

func GetChannelLoggerFromProvaleChain(relayLogger *logger.RelayLogger, src, dst *ProvableChain) *logger.RelayLogger {
	return logger.GetChannelLogger(
		relayLogger,
		src.ChainID(), src.Path().ChannelID, src.Path().PortID,
		dst.ChainID(), dst.Path().ChannelID, dst.Path().PortID,
	)
}
