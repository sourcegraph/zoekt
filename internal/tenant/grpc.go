package tenant

import (
	"context"
	"fmt"
	"runtime/pprof"
	"strconv"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"

	"github.com/sourcegraph/log"
	"github.com/sourcegraph/zoekt/grpc/propagator"
	"github.com/sourcegraph/zoekt/internal/tenant/internal/tenanttype"
	"github.com/sourcegraph/zoekt/internal/tokensvc"
)

const (
	// headerKeyTenanToken is the header key for the signed tenant ID.
	headerKeyTenantToken = "X-Sourcegraph-Tenant-Token"

	// headerKeyTenantID is the header key for the tenant ID.
	headerKeyTenantID = "X-Sourcegraph-Tenant-ID"

	// headerValueNoTenant indicates the request has no tenant.
	headerValueNoTenant = "none"
)

type TenantToken struct {
	TenantID string `json:"tenantID"`
}

// Propagator implements the propagator.Propagator interface
// for propagating tenants across RPC calls. This is modeled directly on
// the HTTP middleware in this package, and should work exactly the same.
type Propagator struct {
	tokenGenerator tokensvc.TokenGenerator
	tokenValidator tokensvc.TokenValidator
	subject        string
}

var _ propagator.Propagator

func NewPropagator() *Propagator {
	subject := "zoekt"
	return &Propagator{
		tokenGenerator: tokensvc.NewTokenGenerator(),
		tokenValidator: tokensvc.NewTokenValidator(subject),
		subject:        subject,
	}
}

func (p Propagator) FromContext(ctx context.Context) metadata.MD {
	md := make(metadata.MD)

	tokenData := TenantToken{}

	tenant, ok := tenanttype.GetTenant(ctx)
	if !ok {
		md.Append(headerKeyTenantID, headerValueNoTenant)
	} else {
		md.Append(headerKeyTenantID, strconv.Itoa(tenant.ID()))
	}

	token, err := p.tokenGenerator.SignData(p.subject, tokenData, "tenant")
	if err != nil {
		log.Error(err)
	}

	md.Append(headerKeyTenantToken, token)
	return md
}

func (p Propagator) InjectContext(ctx context.Context, md metadata.MD) (context.Context, error) {
	var token string
	if vals := md.Get(headerKeyTenantToken); len(vals) > 0 {
		token = vals[0]
	}

	tenantID := ""
	if token != "" {
		tokenData, err := p.tokenValidator.ValidateAndParseToken(token)
		if err != nil {
			return ctx, status.New(codes.Unauthenticated, fmt.Errorf("invalid tenant token: %v", err).Error()).Err()
		}

		tenantToken := TenantToken{}
		if err := tokenData.Get("tenant", &tenantToken); err != nil {
			return ctx, status.New(codes.Unauthenticated, fmt.Errorf("invalid tenant data: %v", err).Error()).Err()
		}

		tenantID = tenantToken.TenantID
	}

	if vals := md.Get(headerKeyTenantID); len(vals) > 0 {
		tenantID = vals[0]
	}

	switch tenantID {
	case "", headerValueNoTenant:
		// Nothing to do, empty tenant.
		return ctx, nil
	default:
		tenant, err := tenanttype.Unmarshal(tenantID)
		if err != nil {
			// The tenant value is invalid.
			return ctx, status.New(codes.InvalidArgument, fmt.Errorf("bad tenant value in metadata: %w", err).Error()).Err()
		}
		return tenanttype.WithTenant(ctx, tenant), nil
	}
}

// UnaryServerInterceptor is a grpc.UnaryServerInterceptor that injects the tenant ID
// from the context into pprof labels.
func UnaryServerInterceptor(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (response any, err error) {
	if tnt, ok := tenanttype.GetTenant(ctx); ok {
		defer pprof.SetGoroutineLabels(ctx)
		ctx = pprof.WithLabels(ctx, pprof.Labels("tenant", tenanttype.Marshal(tnt)))
		pprof.SetGoroutineLabels(ctx)
	}

	return handler(ctx, req)
}

// StreamServerInterceptor is a grpc.StreamServerInterceptor that injects the tenant ID
// from the context into pprof labels.
func StreamServerInterceptor(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if tnt, ok := tenanttype.GetTenant(ss.Context()); ok {
		ctx := ss.Context()
		defer pprof.SetGoroutineLabels(ctx)
		ctx = pprof.WithLabels(ctx, pprof.Labels("tenant", tenanttype.Marshal(tnt)))

		pprof.SetGoroutineLabels(ctx)

		ss = &grpc_middleware.WrappedServerStream{
			ServerStream:   ss,
			WrappedContext: ctx,
		}
	}

	return handler(srv, ss)
}
