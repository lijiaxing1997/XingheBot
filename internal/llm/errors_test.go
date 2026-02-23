package llm

import "testing"

func TestIsLikelyContextOverflowText(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{
			name: "explicit_max_context",
			msg:  "This model's maximum context length is 8192 tokens. However, you requested 9000 tokens (8000 in the messages, 1000 in the completion).",
			want: true,
		},
		{
			name: "context_length_exceeded",
			msg:  "context length exceeded",
			want: true,
		},
		{
			name: "request_too_large",
			msg:  "request_too_large: context window exceeded",
			want: true,
		},
		{
			name: "http_413_too_large",
			msg:  "HTTP 413 Request Too Large: request size exceeds maximum context window",
			want: true,
		},
		{
			name: "context_window_too_small_not_overflow",
			msg:  "context window too small; minimum is 1024 tokens",
			want: false,
		},
		{
			name: "rate_limit_not_overflow",
			msg:  "request reached organization TPD rate limit",
			want: false,
		},
		{
			name: "empty",
			msg:  "   ",
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsLikelyContextOverflowText(tc.msg); got != tc.want {
				t.Fatalf("IsLikelyContextOverflowText() = %v, want %v; msg=%q", got, tc.want, tc.msg)
			}
		})
	}
}
