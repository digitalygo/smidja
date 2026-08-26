package authstore

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
