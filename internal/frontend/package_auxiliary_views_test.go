package frontend

import (
	"testing"

	"github.com/ianbruene/ddgo/internal/app"
)

func TestPackageAuxiliaryViewsAreScopedAndStaleSafe(t *testing.T) {
	var views PackageAuxiliaryViews
	owner := app.DocumentID(7)
	a := PackageStepExecutionSessionID(owner, 1, "cut")
	b := PackageStepExecutionSessionID(owner, 2, "cut")
	first := &fakeDocumentWindow{}
	token, existing, added, err := views.Add(owner, a, first)
	if err != nil || !added || existing != nil || views.Len(owner) != 1 {
		t.Fatalf("first Add = token=%d existing=%v added=%v err=%v", token, existing, added, err)
	}
	if _, existing, added, err = views.Add(owner, a, &fakeDocumentWindow{}); err != nil || added || existing != first {
		t.Fatalf("duplicate Add = existing=%v added=%v err=%v", existing, added, err)
	}
	if _, _, added, err = views.Add(owner, b, &fakeDocumentWindow{}); err != nil || !added || views.Len(owner) != 2 {
		t.Fatalf("distinct step Add added=%v err=%v len=%d", added, err, views.Len(owner))
	}
	if views.Remove(owner, a, token+1) {
		t.Fatal("stale token removed current auxiliary view")
	}
	if !views.Remove(owner, a, token) || views.Len(owner) != 1 {
		t.Fatal("exact token did not remove only matching view")
	}
	if _, _, _, err := views.Add(owner, StandaloneExecutionSessionID(owner), &fakeDocumentWindow{}); err == nil {
		t.Fatal("standalone session accepted as package auxiliary")
	}
}

func TestPackageAuxiliaryViewsRemoveOwnerDoesNotAffectOtherPackages(t *testing.T) {
	var views PackageAuxiliaryViews
	a := PackageStepExecutionSessionID(1, 1, "a")
	b := PackageStepExecutionSessionID(2, 1, "a")
	if _, _, _, err := views.Add(1, a, &fakeDocumentWindow{}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := views.Add(2, b, &fakeDocumentWindow{}); err != nil {
		t.Fatal(err)
	}
	if got := views.RemoveOwner(1); len(got) != 1 || views.Len(1) != 0 || views.Len(2) != 1 {
		t.Fatalf("RemoveOwner result=%d lengths=%d/%d", len(got), views.Len(1), views.Len(2))
	}
}
