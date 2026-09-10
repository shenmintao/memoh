package bridge

import (
	"context"
	"encoding/json"
	"errors"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// CapabilityList and CapabilityCall use the optional service declared in
// packages/runtime/src/capabilities.proto over the existing authenticated link.
// Old runtimes answer Unimplemented without affecting filesystem/exec support.
func (c *Client) CapabilityList(ctx context.Context, result any) error {
	return c.capabilityRPC(ctx, "List", &emptypb.Empty{}, result)
}

func (c *Client) CapabilityCall(ctx context.Context, request, result any) error {
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	return c.capabilityRPC(ctx, "Call", wrapperspb.String(string(raw)), result)
}

func (c *Client) capabilityRPC(ctx context.Context, method string, request, result any) error {
	conn := c.capabilityConn
	if conn == nil {
		conn = c.conn
	}
	response := &wrapperspb.StringValue{}
	if err := conn.Invoke(ctx, "/memoh.runtime.v1.CapabilityService/"+method, request, response); err != nil {
		return err
	}
	if len(response.Value) > 4*1024*1024 {
		return errors.New("local capability result exceeds 4 MiB")
	}
	return json.Unmarshal([]byte(response.Value), result)
}
