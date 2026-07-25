package dataplatform

import "testing"

func TestEstimatePublicAPICostUsesCachedAndLongContextRates(t *testing.T) {
	price := publicAPIPrices["gpt-5.6-sol"]

	upperBound, method := estimatePublicAPICost(price, 1000, 100, nil)
	if upperBound != 8000 || method != "public_api_uncached_upper_bound" {
		t.Fatalf("uncached upper bound = (%d,%q), want (8000,public_api_uncached_upper_bound)", upperBound, method)
	}

	cached := int64(800)
	exact, method := estimatePublicAPICost(price, 1000, 100, &cached)
	if exact != 4400 || method != "public_api_token_rates" {
		t.Fatalf("cached estimate = (%d,%q), want (4400,public_api_token_rates)", exact, method)
	}

	longContext, _ := estimatePublicAPICost(price, 300000, 100, nil)
	if longContext != 3004500 {
		t.Fatalf("long-context estimate = %d, want 3004500", longContext)
	}
}
