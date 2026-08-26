package authstore

// ResolveCredential returns the API key for a provider with environment
// precedence: the environment variable envVarName wins when set (non-
// empty), otherwise the store entry's Key is used. ok is false when
// neither source provides a key. A nil env function or nil store is
// treated as an absent source.
func ResolveCredential(provider, envVarName string, store *Store, env func(string) string) (string, bool) {
	if env != nil && envVarName != "" {
		if v := env(envVarName); v != "" {
			return v, true
		}
	}
	if store != nil {
		if e, ok := store.Get(provider); ok && e.Key != "" {
			return e.Key, true
		}
	}
	return "", false
}
