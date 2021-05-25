package client

import (
	"encoding/base64"
	"fmt"
	"net"

	"github.com/ssb-ngi-pointer/netsim/internal/keys"
	"go.cryptoscope.co/muxrpc/v2"
	"go.cryptoscope.co/netwrap"
	"go.cryptoscope.co/secretstream"
)

const defaultShsCap = "1KHLiKZvAvjbY1ziZEHMXawbCEIM6qwjCDm3VYRan/s="

func NewTCP(port int, secretPath string) (muxrpc.Endpoint, error) {
	kp, err := keys.LoadKeyPair(secretPath)
	if err != nil {
		return nil, err
	}

	// default app key for the secret-handshake connection
	appkey, err := base64.StdEncoding.DecodeString(defaultShsCap)
	if err != nil {
		return nil, err
	}

	// create a shs client to authenticate and encrypt the connection
	clientSHS, err := secretstream.NewClient(kp.Pair, appkey)
	if err != nil {
		return nil, err
	}

	tcpAddr, err := net.ResolveTCPAddr("tcp4", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		return nil, err
	}

	// returns a new connection that went through shs and does boxstream
	authedConn, err := netwrap.Dial(tcpAddr, clientSHS.ConnWrapper(kp.Feed.PubKey()))
	if err != nil {
		return nil, err
	}

	var muxMock = new(muxrpc.FakeHandler)
	muxClient := muxrpc.Handle(muxrpc.NewPacker(authedConn), muxMock)

	// TODO: how to waitgroups
	go func() {
		srv := muxClient.(muxrpc.Server)
		err = srv.Serve()
		if err != nil {
			fmt.Printf("mux client(%d) error: %v\n", port, err)
		}
	}()

	return muxClient, nil
}
