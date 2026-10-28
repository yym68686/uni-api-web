package main

// This index belongs to one gateway response, never to a shared cache. The
// first nonempty rule for a scope wins, even if it omits the queried provider.
type subChannelRuleIndex map[[2]string]map[string]int

// Build an index only when multiple owned channels will reuse it. A single
// channel keeps the original scan, avoiding index allocation for unused rules.
func subChannelPositionsForOwned(state subGatewayControls, owned map[string]subChannelRef) []map[string]int {
	eligible := 0
	for _, channel := range state.Temporary {
		if _, exists := owned[channel.Provider]; exists && !channel.IdentityChanged {
			eligible++
		}
	}
	positions := make([]map[string]int, len(state.Temporary))
	var index subChannelRuleIndex
	if eligible > 1 {
		index = newSubChannelRuleIndex(state)
	}
	for i, channel := range state.Temporary {
		if _, exists := owned[channel.Provider]; !exists || channel.IdentityChanged {
			continue
		}
		if index != nil {
			positions[i] = index.positions(channel.KeyID, channel.Provider, channel.Models)
		} else {
			positions[i] = scanSubChannelPositions(state, channel.KeyID, channel.Provider, channel.Models)
		}
	}
	return positions
}

func scanSubChannelPositions(state subGatewayControls, key, provider string, models []string) map[string]int {
	positions := map[string]int{}
	for _, model := range models {
		for _, scope := range [4][2]string{{key, model}, {key, ""}, {"", model}, {"", ""}} {
			found := false
			for _, rule := range state.Rules {
				if rule.KeyID != scope[0] || rule.Model != scope[1] || len(rule.Order) == 0 {
					continue
				}
				for i, name := range rule.Order {
					if name == provider {
						positions[model] = i + 1
					}
				}
				found = true
				break
			}
			if found {
				break
			}
		}
	}
	return positions
}

func newSubChannelRuleIndex(state subGatewayControls) subChannelRuleIndex {
	index := make(subChannelRuleIndex, len(state.Rules))
	for _, rule := range state.Rules {
		scope := [2]string{rule.KeyID, rule.Model}
		if len(rule.Order) == 0 || index[scope] != nil {
			continue
		}
		positions := make(map[string]int, len(rule.Order))
		for i, provider := range rule.Order {
			// Preserve the last position when a rule repeats a provider.
			positions[provider] = i + 1
		}
		index[scope] = positions
	}
	return index
}

func (index subChannelRuleIndex) positions(key, provider string, models []string) map[string]int {
	positions := map[string]int{}
	for _, model := range models {
		for _, scope := range [4][2]string{{key, model}, {key, ""}, {"", model}, {"", ""}} {
			if rule, exists := index[scope]; exists {
				if position, found := rule[provider]; found {
					positions[model] = position
				}
				break
			}
		}
	}
	return positions
}
