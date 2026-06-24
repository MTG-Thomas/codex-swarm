package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync/atomic"
)

// Client is the narrow boundary around Codex app-server JSON-RPC.
type Client struct {
	in     io.Writer
	out    *json.Decoder
	nextID atomic.Int64
}

func NewClient(in io.Writer, out io.Reader) *Client {
	return &Client{
		in:  in,
		out: json.NewDecoder(out),
	}
}

func (c *Client) Call(ctx context.Context, method string, params any) (*Response, error) {
	id := c.nextID.Add(1)
	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')

	type result struct {
		response *Response
		err      error
	}
	ch := make(chan result, 1)
	go func() {
		if _, err := c.in.Write(data); err != nil {
			ch <- result{err: err}
			return
		}
		var resp Response
		if err := c.out.Decode(&resp); err != nil {
			ch <- result{err: err}
			return
		}
		if resp.Error != nil {
			ch <- result{err: fmt.Errorf("app-server %s failed: %s", method, resp.Error.Message)}
			return
		}
		ch <- result{response: &resp}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		return res.response, res.err
	}
}
