package observability

import "testing"

// TestQualifyComponentIdentityIsIdempotent pins the property the Skill
// observatory's identity resolution actually depends on: applying an owner
// plugin's namespace twice must equal applying it once.
//
// Before this held, Claude Code's already-qualified skill.name
// ("sre-agent:verification-strategy") had its owner prepended a second time,
// producing "sre-agent:sre-agent:verification-strategy". The data platform's
// resolver compares against owner.declared_name || ':' || component
// .declared_name, so a doubled identity matched nothing -- every Claude skill
// invocation resolved to zero candidates and was excluded from every count.
func TestQualifyComponentIdentityIsIdempotent(t *testing.T) {
	for _, test := range []struct {
		name     string
		owner    string
		identity string
		want     string
	}{
		{
			// The pre-existing shape: a bare identity with the owner carried
			// separately. This is what the fixture lane and Codex's rollout
			// evidence send, and it must keep qualifying exactly as before.
			name: "bare identity is qualified", owner: "owner-plugin",
			identity: "plugin-skill", want: "owner-plugin:plugin-skill",
		},
		{
			// The real Claude Code 2.1.220 wire shape.
			name: "already owner-qualified identity is left alone", owner: "sre-agent",
			identity: "sre-agent:verification-strategy",
			want:     "sre-agent:verification-strategy",
		},
		{
			// An owner spelled in its marketplace-qualified form is the same
			// owner; the identity must not gain a second namespace either way.
			name: "marketplace-qualified owner recognises its own prefix",
			owner: "t-skills-kotlin@yuzuru-engineering", identity: "t-skills-kotlin:kotlin-jpa-entity",
			want: "t-skills-kotlin:kotlin-jpa-entity",
		},
		{
			name: "marketplace-qualified identity is left alone", owner: "t-skills-kotlin",
			identity: "t-skills-kotlin@yuzuru-engineering:kotlin-jpa-entity",
			want:     "t-skills-kotlin@yuzuru-engineering:kotlin-jpa-entity",
		},
		{
			// A user-scope skill has no owner at all.
			name: "absent owner never qualifies", owner: "",
			identity: "write-kotlin", want: "write-kotlin",
		},
		{
			name: "absent identity stays absent", owner: "owner-plugin",
			identity: "", want: "",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			once := qualifyComponentIdentity(test.owner, test.identity)
			if once != test.want {
				t.Fatalf("qualifyComponentIdentity(%q,%q)=%q want %q",
					test.owner, test.identity, once, test.want)
			}
			if twice := qualifyComponentIdentity(test.owner, once); twice != once {
				t.Fatalf("not idempotent: f(o,f(o,x))=%q f(o,x)=%q", twice, once)
			}
		})
	}
}

// TestQualifyComponentIdentityNeverDoublesAnOwnerPrefix states the invariant
// directly rather than by example: for every owner/identity pair, the result
// carries at most one owner namespace, because a second application is a
// no-op. An inventory declared_name can never contain ':' (both adapters'
// SKILL.md frontmatter parsers bound it to [A-Za-z0-9][A-Za-z0-9._-]{0,127}),
// which is what makes the ':' test exact rather than heuristic.
func TestQualifyComponentIdentityNeverDoublesAnOwnerPrefix(t *testing.T) {
	owners := []string{"", "owner", "owner@marketplace", "t-skills-kotlin"}
	identities := []string{
		"", "skill", "owner:skill", "other:skill",
		"owner@marketplace:skill", "t-skills-kotlin:kotlin-jpa-entity",
	}
	for _, owner := range owners {
		for _, identity := range identities {
			first := qualifyComponentIdentity(owner, identity)
			second := qualifyComponentIdentity(owner, first)
			if first != second {
				t.Fatalf("owner=%q identity=%q: f=%q f∘f=%q", owner, identity, first, second)
			}
		}
	}
}
