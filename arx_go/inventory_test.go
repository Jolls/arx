package main

import "testing"

func TestLedgerWithBalances(t *testing.T) {
	// Oldest→newest qtys: +10 (receipt), -3 (issue), +5 (adjustment) → balances 10, 7, 12.
	asc := []InventoryTxnView{
		{Type: "receipt", Qty: 10},
		{Type: "issue", Qty: -3},
		{Type: "adjustment", Qty: 5},
	}
	got := ledgerWithBalances(asc)

	// Returned newest-first.
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3", len(got))
	}
	if got[0].Type != "adjustment" || got[2].Type != "receipt" {
		t.Errorf("order = %s..%s, want newest-first (adjustment..receipt)", got[0].Type, got[2].Type)
	}
	// Running balance is as-of each transaction (computed oldest→newest).
	wantBal := map[string]float64{"receipt": 10, "issue": 7, "adjustment": 12}
	for _, v := range got {
		if v.Balance != wantBal[v.Type] {
			t.Errorf("%s balance = %.2f, want %.2f", v.Type, v.Balance, wantBal[v.Type])
		}
	}
	// Final on-hand = last (newest) row's balance.
	if got[0].Balance != 12 {
		t.Errorf("final balance = %.2f, want 12", got[0].Balance)
	}
}

func TestLedgerWithBalancesEmpty(t *testing.T) {
	if got := ledgerWithBalances(nil); len(got) != 0 {
		t.Errorf("empty ledger returned %d rows, want 0", len(got))
	}
}
