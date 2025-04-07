// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.3.0
// - protoc             (unknown)
// source: sourcegraph/zoekt/configuration/v1/configuration.proto

package v1

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

const (
	ZoektConfigurationService_SearchConfiguration_FullMethodName = "/sourcegraph.zoekt.configuration.v1.ZoektConfigurationService/SearchConfiguration"
	ZoektConfigurationService_List_FullMethodName                = "/sourcegraph.zoekt.configuration.v1.ZoektConfigurationService/List"
	ZoektConfigurationService_UpdateIndexStatus_FullMethodName   = "/sourcegraph.zoekt.configuration.v1.ZoektConfigurationService/UpdateIndexStatus"
)

// ZoektConfigurationServiceClient is the client API for ZoektConfigurationService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type ZoektConfigurationServiceClient interface {
	// SearchConfiguration returns the current indexing configuration for the specified repositories.
	SearchConfiguration(ctx context.Context, in *SearchConfigurationRequest, opts ...grpc.CallOption) (*SearchConfigurationResponse, error)
	// List returns the list of repositories that the client should index.
	List(ctx context.Context, in *ListRequest, opts ...grpc.CallOption) (*ListResponse, error)
	// UpdateIndexStatus informs the server that the caller has indexed the specified repositories
	// at the specified commits.
	UpdateIndexStatus(ctx context.Context, in *UpdateIndexStatusRequest, opts ...grpc.CallOption) (*UpdateIndexStatusResponse, error)
}

type zoektConfigurationServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewZoektConfigurationServiceClient(cc grpc.ClientConnInterface) ZoektConfigurationServiceClient {
	return &zoektConfigurationServiceClient{cc}
}

func (c *zoektConfigurationServiceClient) SearchConfiguration(ctx context.Context, in *SearchConfigurationRequest, opts ...grpc.CallOption) (*SearchConfigurationResponse, error) {
	out := new(SearchConfigurationResponse)
	err := c.cc.Invoke(ctx, ZoektConfigurationService_SearchConfiguration_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *zoektConfigurationServiceClient) List(ctx context.Context, in *ListRequest, opts ...grpc.CallOption) (*ListResponse, error) {
	out := new(ListResponse)
	err := c.cc.Invoke(ctx, ZoektConfigurationService_List_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *zoektConfigurationServiceClient) UpdateIndexStatus(ctx context.Context, in *UpdateIndexStatusRequest, opts ...grpc.CallOption) (*UpdateIndexStatusResponse, error) {
	out := new(UpdateIndexStatusResponse)
	err := c.cc.Invoke(ctx, ZoektConfigurationService_UpdateIndexStatus_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ZoektConfigurationServiceServer is the server API for ZoektConfigurationService service.
// All implementations must embed UnimplementedZoektConfigurationServiceServer
// for forward compatibility
type ZoektConfigurationServiceServer interface {
	// SearchConfiguration returns the current indexing configuration for the specified repositories.
	SearchConfiguration(context.Context, *SearchConfigurationRequest) (*SearchConfigurationResponse, error)
	// List returns the list of repositories that the client should index.
	List(context.Context, *ListRequest) (*ListResponse, error)
	// UpdateIndexStatus informs the server that the caller has indexed the specified repositories
	// at the specified commits.
	UpdateIndexStatus(context.Context, *UpdateIndexStatusRequest) (*UpdateIndexStatusResponse, error)
	mustEmbedUnimplementedZoektConfigurationServiceServer()
}

// UnimplementedZoektConfigurationServiceServer must be embedded to have forward compatible implementations.
type UnimplementedZoektConfigurationServiceServer struct {
}

func (UnimplementedZoektConfigurationServiceServer) SearchConfiguration(context.Context, *SearchConfigurationRequest) (*SearchConfigurationResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method SearchConfiguration not implemented")
}
func (UnimplementedZoektConfigurationServiceServer) List(context.Context, *ListRequest) (*ListResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method List not implemented")
}
func (UnimplementedZoektConfigurationServiceServer) UpdateIndexStatus(context.Context, *UpdateIndexStatusRequest) (*UpdateIndexStatusResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method UpdateIndexStatus not implemented")
}
func (UnimplementedZoektConfigurationServiceServer) mustEmbedUnimplementedZoektConfigurationServiceServer() {
}

// UnsafeZoektConfigurationServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to ZoektConfigurationServiceServer will
// result in compilation errors.
type UnsafeZoektConfigurationServiceServer interface {
	mustEmbedUnimplementedZoektConfigurationServiceServer()
}

func RegisterZoektConfigurationServiceServer(s grpc.ServiceRegistrar, srv ZoektConfigurationServiceServer) {
	s.RegisterService(&ZoektConfigurationService_ServiceDesc, srv)
}

func _ZoektConfigurationService_SearchConfiguration_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(SearchConfigurationRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ZoektConfigurationServiceServer).SearchConfiguration(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ZoektConfigurationService_SearchConfiguration_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ZoektConfigurationServiceServer).SearchConfiguration(ctx, req.(*SearchConfigurationRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ZoektConfigurationService_List_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ZoektConfigurationServiceServer).List(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ZoektConfigurationService_List_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ZoektConfigurationServiceServer).List(ctx, req.(*ListRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ZoektConfigurationService_UpdateIndexStatus_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UpdateIndexStatusRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ZoektConfigurationServiceServer).UpdateIndexStatus(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: ZoektConfigurationService_UpdateIndexStatus_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ZoektConfigurationServiceServer).UpdateIndexStatus(ctx, req.(*UpdateIndexStatusRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// ZoektConfigurationService_ServiceDesc is the grpc.ServiceDesc for ZoektConfigurationService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var ZoektConfigurationService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "sourcegraph.zoekt.configuration.v1.ZoektConfigurationService",
	HandlerType: (*ZoektConfigurationServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "SearchConfiguration",
			Handler:    _ZoektConfigurationService_SearchConfiguration_Handler,
		},
		{
			MethodName: "List",
			Handler:    _ZoektConfigurationService_List_Handler,
		},
		{
			MethodName: "UpdateIndexStatus",
			Handler:    _ZoektConfigurationService_UpdateIndexStatus_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "sourcegraph/zoekt/configuration/v1/configuration.proto",
}
