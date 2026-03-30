package proxy

// MergeRequestMetadata copies an existing metadata map and attaches request
// profile details without mutating the caller's original map.
func MergeRequestMetadata(metadata map[string]any, profile RequestProfile) map[string]any {
	merged := make(map[string]any, len(metadata)+1)
	for key, value := range metadata {
		merged[key] = value
	}

	requestMetadata := map[string]any{}
	if existing, ok := merged["request"].(map[string]any); ok {
		for key, value := range existing {
			requestMetadata[key] = value
		}
	}

	if profile.UserAgent != "" {
		requestMetadata["user_agent"] = profile.UserAgent
	}
	if profile.Proxy != nil {
		requestMetadata["proxy_ip"] = profile.Proxy.IP
		requestMetadata["proxy_port"] = profile.Proxy.Port
		if profile.Proxy.Type != "" {
			requestMetadata["proxy_type"] = profile.Proxy.Type
		}
		if profile.Proxy.HTTPS != "" {
			requestMetadata["proxy_https"] = profile.Proxy.HTTPS
		}
	}

	if len(requestMetadata) > 0 {
		merged["request"] = requestMetadata
	}
	return merged
}
