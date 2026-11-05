package sync

import (
	"testing"

	natsjwt "github.com/nats-io/jwt/v2"
	"github.com/pocketbase/pocketbase/core"
	"github.com/skeeeon/pb-nats/internal/jwt"
	"github.com/skeeeon/pb-nats/internal/nkey"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// signActivations is where the grant on the EXPORT becomes a token in the
// IMPORTER's JWT. It is driven through real records, so allowed_importers crosses
// the record->model boundary the way production reads it: a JSON field reads back
// as types.JSONRaw, and a type assertion to []string would grant nobody.
func TestSignActivationsSignsOnlyForListedImporters(t *testing.T) {
	app := newTestApp(t)
	nm := nkey.NewManager()
	options := pbtypes.Options{
		AccountCollectionName: "nats_accounts",
		ExportCollectionName:  "nats_account_exports",
		ImportCollectionName:  "nats_account_imports",
	}

	accounts := core.NewBaseCollection(options.AccountCollectionName)
	accounts.Fields.Add(&core.TextField{Name: "name", Max: 100})
	accounts.Fields.Add(&core.TextField{Name: "public_key", Max: 200})
	accounts.Fields.Add(&core.TextField{Name: "seed", Max: 200})
	if err := app.Save(accounts); err != nil {
		t.Fatalf("save accounts collection: %v", err)
	}
	exports := core.NewBaseCollection(options.ExportCollectionName)
	exports.Fields.Add(&core.TextField{Name: "account_id", Max: 200})
	exports.Fields.Add(&core.TextField{Name: "name", Max: 100})
	exports.Fields.Add(&core.TextField{Name: "subject", Max: 500})
	exports.Fields.Add(&core.TextField{Name: "type", Max: 20})
	exports.Fields.Add(&core.BoolField{Name: "token_req"})
	exports.Fields.Add(&core.JSONField{Name: "allowed_importers", MaxSize: 50000})
	exports.Fields.Add(&core.JSONField{Name: "revocations", MaxSize: 50000})
	if err := app.Save(exports); err != nil {
		t.Fatalf("save exports collection: %v", err)
	}

	newAccount := func(name string) *core.Record {
		seed, public, _, _, err := nm.GenerateAccountKeyPair()
		if err != nil {
			t.Fatalf("GenerateAccountKeyPair: %v", err)
		}
		r := core.NewRecord(accounts)
		r.Set("name", name)
		r.Set("public_key", public)
		r.Set("seed", seed)
		if err := app.Save(r); err != nil {
			t.Fatalf("save account %s: %v", name, err)
		}
		return r
	}
	exporter := newAccount("exporter")
	granted := newAccount("granted")
	other := newAccount("other")

	export := core.NewRecord(exports)
	export.Set("account_id", exporter.Id)
	export.Set("name", "inventory-x")
	export.Set("subject", "inv.x.>")
	export.Set("type", "stream")
	export.Set("token_req", true)
	export.Set("allowed_importers", []string{granted.GetString("public_key")})
	if err := app.Save(export); err != nil {
		t.Fatalf("save export: %v", err)
	}

	sm := NewManager(app, jwt.NewGenerator(nm, options), nm, nil, options, "")
	importOf := func(subject string) []*pbtypes.AccountImportRecord {
		return []*pbtypes.AccountImportRecord{{
			Name: "inventory-events", Subject: subject, Account: exporter.GetString("public_key"), Type: "stream",
		}}
	}

	imports := importOf("inv.x.>")
	sm.signActivations(granted.GetString("public_key"), imports)
	if imports[0].Token == "" {
		t.Fatal("no token signed for an importer the export lists")
	}
	act, err := natsjwt.DecodeActivationClaims(imports[0].Token)
	if err != nil {
		t.Fatalf("decode activation: %v", err)
	}
	if act.Subject != granted.GetString("public_key") || act.Issuer != exporter.GetString("public_key") {
		t.Errorf("activation subject/issuer = %s/%s, want the importer/the exporter's identity key", act.Subject, act.Issuer)
	}

	for _, tc := range []struct {
		what     string
		importer string
		subject  string
	}{
		{"an importer the export does not list", other.GetString("public_key"), "inv.x.>"},
		{"a listed importer asking for a wider subject", granted.GetString("public_key"), "inv.>"},
	} {
		imps := importOf(tc.subject)
		sm.signActivations(tc.importer, imps)
		if imps[0].Token != "" {
			t.Errorf("%s got a token; the server would then admit it", tc.what)
		}
	}
}

func TestExportRevocationsAfter(t *testing.T) {
	revs, changed := exportRevocationsAfter([]string{"A", "B"}, []string{"B"}, nil, 100)
	if !changed || revs["A"] != 100 || len(revs) != 1 {
		t.Errorf("removing A: revs = %v changed = %v, want A revoked at 100", revs, changed)
	}

	revs, changed = exportRevocationsAfter([]string{"B"}, []string{"A", "B"}, revs, 200)
	if !changed || len(revs) != 0 {
		t.Errorf("putting A back: revs = %v changed = %v, want A cleared", revs, changed)
	}

	if _, changed := exportRevocationsAfter([]string{"A"}, []string{"A"}, map[string]int64{"Z": 5}, 300); changed {
		t.Error("an unchanged list reported a change, which would rewrite the export on every save")
	}
}
