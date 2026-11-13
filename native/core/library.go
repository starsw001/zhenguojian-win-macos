package core

// dramaProvider 按作品的站源标识选择对应处理器。
func dramaProvider(drama Drama) string {
	if source := canonicalProviderSource(drama.Source); source != "" {
		return source
	}
	if source, _, ok := splitProviderDramaID(drama.ID); ok {
		return source
	}
	return "cloudfront"
}
