package authz

import "testing"

// TestRequire_CatalogUpdate: PATCH /catalog/addons/{name} is Operator+,
// same tier as its v3 sibling addon.update-catalog.
func TestRequire_CatalogUpdate(t *testing.T) {
	action := "catalog.update"

	if !Require(makeRequest("admin-user", "admin"), action) {
		t.Error("admin should be allowed to edit a catalog entry")
	}
	if !Require(makeRequest("op-user", "operator"), action) {
		t.Error("operator should be allowed to edit a catalog entry")
	}
	if Require(makeRequest("view-user", "viewer"), action) {
		t.Error("viewer should be denied editing a catalog entry")
	}
}

// TestRequire_CatalogRemove: DELETE /catalog/addons/{name} is Admin-only,
// same tier as its v3 sibling addon.remove-from-catalog.
func TestRequire_CatalogRemove(t *testing.T) {
	action := "catalog.remove"

	if !Require(makeRequest("admin-user", "admin"), action) {
		t.Error("admin should be allowed to delete a catalog entry")
	}
	if Require(makeRequest("op-user", "operator"), action) {
		t.Error("operator should be denied deleting a catalog entry")
	}
	if Require(makeRequest("view-user", "viewer"), action) {
		t.Error("viewer should be denied deleting a catalog entry")
	}
}
