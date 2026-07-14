package site

import "context"

type accessProfileKey string

const ctxAccessProfile accessProfileKey = "access_profile"

const AccessProfileReader = "reader"

func WithAccessProfile(ctx context.Context, profile string) context.Context {
	if ctx == nil || profile == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxAccessProfile, profile)
}

func AccessProfileFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	profile, _ := ctx.Value(ctxAccessProfile).(string)
	return profile
}

func IsReaderProfile(ctx context.Context) bool {
	return AccessProfileFromContext(ctx) == AccessProfileReader
}

// ReaderSafeResolvedPage projects a resolved page into a reader-safe public-only
// view. Reader callers must never receive source-only content through tools that
// are made visible via the public-safe read catalog.
func ReaderSafeResolvedPage(resolved ResolvedPage) (ResolvedPage, bool) {
	if resolved.Public == nil {
		return ResolvedPage{}, false
	}
	return ResolvedPage{Public: resolved.Public}, true
}
