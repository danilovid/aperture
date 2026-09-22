package postgres

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// One organization must never see another's traffic, and the only thing
// standing between them is an org_id condition in every query. Reviews miss a
// missing WHERE clause; this test does not.
//
// It reads the SQL in this package as text: every statement that touches a
// table whose rows belong to an organization has to name org_id, as a
// predicate when it reads or changes rows and as a column when it inserts
// them. An insert that leaves org_id out would be filed under the column
// default — silently, in somebody else's organization.

// orgScopedTables hold one organization's data. provider_keys is deliberately
// absent: its rows hang off an api_keys row by id, and that row is only ever
// reached through a query checked here or through the token hash itself.
var orgScopedTables = []string{
	"api_keys",
	"dlp_events",
	"dlp_policies",
	"key_limits",
	"request_logs",
}

// isolationExempt lists the statements that may touch an organization-scoped
// table without naming an organization, and why. A statement not in this list
// and not carrying org_id fails the test, so adding an exemption is a change
// somebody has to make on purpose and explain here.
var isolationExempt = []struct{ fragment, why string }{
	{
		fragment: "FROM api_keys WHERE key_hash = $1",
		why: "authentication: the token decides which organization the caller " +
			"is in, so this is the one lookup that cannot already know it",
	},
	{
		fragment: "UPDATE api_keys SET key_hash = 'config:' || org_id::text",
		why:      "one-off migration retiring the shared dev key in every organization",
	},
}

// sqlVerb finds the first statement keyword, which decides what we demand.
var sqlVerb = regexp.MustCompile(`(?i)\b(SELECT|INSERT INTO|UPDATE|DELETE FROM)\b`)

// orgPredicate is an actual condition, not a mention: "org_id::text" in a
// SELECT list must not be mistaken for a filter.
var orgPredicate = regexp.MustCompile(`(?i)\borg_id\s*=\s*\$`)

// orgColumn matches org_id named in an INSERT's column list, which is how an
// insert says which organization the new row belongs to.
var orgColumn = regexp.MustCompile(`(?i)INSERT\s+INTO\s+\w+\s*\([^)]*\borg_id\b`)

func TestEveryQueryIsScopedByOrganization(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatalf("parse the package: %v", err)
	}

	checked := 0
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				sql := lit.Value
				if !sqlVerb.MatchString(sql) {
					return true
				}
				table := scopedTableIn(sql)
				if table == "" || isSchema(sql) {
					return true
				}
				if why := exemptionFor(sql); why != "" {
					checked++
					return true
				}
				checked++
				if complaint := violation(sql, table); complaint != "" {
					t.Errorf("%s: %s:\n%s", fset.Position(lit.Pos()), complaint, indent(sql))
				}
				return true
			})
		}
	}

	// A test that silently checks nothing is worse than no test: if the SQL
	// moves somewhere this cannot see, say so rather than pass.
	if checked < len(orgScopedTables) {
		t.Fatalf("only %d statements were checked; the queries are no longer where this test looks", checked)
	}
	t.Logf("checked %d statements against %d organization-scoped tables", checked, len(orgScopedTables))
}

// violation returns what is wrong with a statement against an
// organization-scoped table, or "" when it is properly scoped.
func violation(sql, table string) string {
	if strings.EqualFold(sqlVerb.FindString(sql), "insert into") {
		if !orgColumn.MatchString(sql) {
			return "this insert into " + table + " does not name org_id, so the new row " +
				"would be filed under the column default"
		}
		return ""
	}
	if !orgPredicate.MatchString(sql) {
		return "this statement reads or changes " + table + " without an org_id condition, " +
			"so it would cross organizations"
	}
	return ""
}

func scopedTableIn(sql string) string {
	lower := strings.ToLower(sql)
	for _, table := range orgScopedTables {
		if regexp.MustCompile(`\b` + table + `\b`).MatchString(lower) {
			return table
		}
	}
	return ""
}

// isSchema reports whether the literal is DDL — a table definition or a
// migration block — rather than a query against existing rows.
func isSchema(sql string) bool {
	upper := strings.ToUpper(sql)
	for _, marker := range []string{"CREATE TABLE", "CREATE INDEX", "ALTER TABLE", "DO $$", "INFORMATION_SCHEMA"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

func exemptionFor(sql string) string {
	for _, e := range isolationExempt {
		if strings.Contains(sql, e.fragment) {
			return e.why
		}
	}
	return ""
}

func indent(sql string) string {
	return "\t" + strings.ReplaceAll(strings.TrimSpace(strings.Trim(sql, "`\"")), "\n", "\n\t")
}

// A query can be perfectly scoped and still fail against a database created
// before multi-tenancy, because the column it filters on was never added.
// Every organization-scoped table is declared in one file; the migration that
// gives it org_id belongs in that same file, next to it.
func TestEveryScopedTableIsMigrated(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list the package: %v", err)
	}
	for _, table := range orgScopedTables {
		declaredIn, migratedIn := "", ""
		for _, name := range files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			src, err := os.ReadFile(name)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			text := string(src)
			if strings.Contains(text, "CREATE TABLE IF NOT EXISTS "+table) {
				declaredIn = name
			}
			if strings.Contains(text, `addOrgColumn(ctx, pool, "`+table+`"`) {
				migratedIn = name
			}
		}
		if declaredIn == "" {
			t.Errorf("%s is listed as organization-scoped but no file declares it", table)
			continue
		}
		if migratedIn == "" {
			t.Errorf("%s (declared in %s) never gets its org_id column: every query against it "+
				"would fail on a database created before multi-tenancy", table, declaredIn)
			continue
		}
		if migratedIn != declaredIn {
			t.Errorf("%s is declared in %s but migrated in %s; keep the two together so neither "+
				"can be added without the other", table, declaredIn, migratedIn)
		}
	}
}

// The guard above is only worth having if it fails on the mistakes it exists
// to catch, so here are those mistakes.
func TestIsolationGuardCatchesUnscopedSQL(t *testing.T) {
	caught := []string{
		`SELECT id, rule FROM dlp_events WHERE action = $1`,
		`SELECT COUNT(*) FROM request_logs WHERE ts >= $1`,
		`UPDATE key_limits SET limits = $2 WHERE name = $1`,
		`DELETE FROM dlp_policies WHERE name = $1`,
		// org_id is mentioned, but only as an output column.
		`SELECT id, org_id::text FROM dlp_events WHERE rule = $1`,
		`INSERT INTO request_logs (model, provider) VALUES ($1, $2)`,
	}
	for _, sql := range caught {
		table := scopedTableIn(sql)
		if table == "" {
			t.Fatalf("the sample names no scoped table, so the guard would skip it: %s", sql)
		}
		if violation(sql, table) == "" {
			t.Errorf("the guard accepted an unscoped statement: %s", sql)
		}
	}

	allowed := []string{
		`SELECT id FROM dlp_events WHERE org_id = $1::uuid AND rule = $2`,
		`DELETE FROM dlp_policies WHERE org_id = $1::uuid AND name = $2`,
		`INSERT INTO request_logs (org_id, model) VALUES ($1::uuid, $2)`,
		// Nothing here belongs to an organization, so nothing is demanded.
		`SELECT email FROM users WHERE id = $1::uuid`,
	}
	for _, sql := range allowed {
		table := scopedTableIn(sql)
		if table == "" {
			continue
		}
		if complaint := violation(sql, table); complaint != "" {
			t.Errorf("the guard rejected a correctly scoped statement: %s (%s)", sql, complaint)
		}
	}
}
