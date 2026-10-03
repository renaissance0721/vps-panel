package subscription

import (
	"errors"
	"strings"
	"testing"
)

func TestClientTemplatesRemainIndependentForPersonalAndPlan(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	ctx := t.Context()
	insertPersonalTestUser(t, db, 100, "admin", "admin")
	insertSubscriptionTestServer(t, db, 1, "US", "203.0.113.1")
	proxy := createSubscriptionTestRealityProxy(t, db, 1, "US", 443)
	actor := PersonalSubscriptionActor{UserID: 100, Role: "admin"}
	mihomo, err := service.CreateTemplate(ctx, CreateSubscriptionTemplateInput{
		Name: "Mihomo", Type: TemplateTypeMihomo, Enabled: true, Content: "mixed-port: 9999"})
	if err != nil {
		t.Fatal(err)
	}
	shadowrocket, err := service.CreateTemplate(ctx, CreateSubscriptionTemplateInput{
		Name: "Shadowrocket", Type: TemplateTypeShadowrocket, Enabled: true,
		Content: strings.Replace(builtinShadowrocketTemplate, "ipv6 = true", "ipv6 = false", 1)})
	if err != nil {
		t.Fatal(err)
	}
	personal, err := service.CreatePersonalSubscription(ctx, actor, CreatePersonalSubscriptionInput{
		Name: "Personal", ClientName: "default", Enabled: true, MihomoTemplateID: &mihomo.ID, ShadowrocketTemplateID: &shadowrocket.ID})
	if err != nil {
		t.Fatal(err)
	}
	if personal.MihomoTemplateName != "Mihomo" || personal.ShadowrocketTemplateName != "Shadowrocket" {
		t.Fatal("template names missing")
	}
	_, err = service.SetPersonalSubscriptionNodes(ctx, actor, personal.ID, []SetPersonalSubscriptionNodeInput{
		{SourceType: PersonalSourceProxy, SourceID: proxy.ID, DisplayName: "US1", Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := service.GeneratePersonalSubscriptionDataForOwner(ctx, actor, personal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if data.MihomoTemplate == nil || data.ShadowrocketTemplate == nil {
		t.Fatal("independent templates not loaded")
	}
	mihomoBody, err := RenderPersonalMihomoSubscription(data)
	if err != nil || !strings.Contains(string(mihomoBody), "mixed-port: 9999") {
		t.Fatal("Mihomo template not applied", err)
	}
	shadowrocketBody, err := RenderPersonalShadowrocketSubscription(data)
	if err != nil || !strings.Contains(string(shadowrocketBody), "ipv6 = false") {
		t.Fatal("Shadowrocket template not applied", err)
	}

	plan, err := service.CreatePlan(ctx, CreatePlanInput{Name: "Shared", Enabled: true, MihomoTemplateID: &mihomo.ID, ShadowrocketTemplateID: &shadowrocket.ID})
	if err != nil {
		t.Fatal(err)
	}
	node, _, err := service.CreatePublishedNode(ctx, CreatePublishedNodeInput{Name: "US1", Mode: NodeModeDirect, TargetProxyID: proxy.ID, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SetPlanNodes(ctx, plan.ID, []int64{node.ID}); err != nil {
		t.Fatal(err)
	}
	insertSubscriptionTestSubscriber(t, db, 101, "subscriber")
	if _, _, err := service.UpdateSubscriber(ctx, 101, UpdateSubscriberInput{PlanIDSet: true, PlanID: &plan.ID}); err != nil {
		t.Fatal(err)
	}
	shared, _, err := service.GenerateSubscriptionDataForUser(ctx, 101)
	if err != nil || shared.MihomoTemplate == nil || shared.ShadowrocketTemplate == nil {
		t.Fatal("plan templates not loaded", err)
	}
	if body, err := RenderMihomoSubscription(shared); err != nil || !strings.Contains(string(body), "mixed-port: 9999") {
		t.Fatal("shared Mihomo", err)
	}
	if body, err := RenderShadowrocketSubscription(shared); err != nil || !strings.Contains(string(body), "ipv6 = false") {
		t.Fatal("shared Shadowrocket", err)
	}
	for _, id := range []int64{mihomo.ID, shadowrocket.ID} {
		if err := service.DeleteTemplate(ctx, id); !errors.Is(err, ErrTemplateReferenced) {
			t.Fatal("referenced template deleted", err)
		}
	}

	// Disable while preserving references. Existing selections remain editable;
	// rendering falls back to that client's built-in template, as Mihomo did.
	disabled := false
	if _, err := service.UpdateTemplate(ctx, shadowrocket.ID, UpdateSubscriptionTemplateInput{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdatePersonalSubscription(ctx, actor, personal.ID, UpdatePersonalSubscriptionInput{
		ShadowrocketTemplateIDSet: true, ShadowrocketTemplateID: &shadowrocket.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.UpdatePlan(ctx, plan.ID, UpdatePlanInput{ShadowrocketTemplateIDSet: true, ShadowrocketTemplateID: &shadowrocket.ID}); err != nil {
		t.Fatal(err)
	}
	data, err = service.GeneratePersonalSubscriptionDataForOwner(ctx, actor, personal.ID)
	if err != nil || data.ShadowrocketTemplate != nil || data.MihomoTemplate == nil {
		t.Fatal("disabled template fallback affected other client", err)
	}
	if _, err := service.CreatePlan(ctx, CreatePlanInput{Name: "Disabled", ShadowrocketTemplateID: &shadowrocket.ID}); !errors.Is(err, ErrTemplateDisabled) {
		t.Fatal(err)
	}
	if _, err := service.CreatePersonalSubscription(ctx, actor, CreatePersonalSubscriptionInput{Name: "Disabled", ClientName: "default", ShadowrocketTemplateID: &shadowrocket.ID}); !errors.Is(err, ErrTemplateDisabled) {
		t.Fatal(err)
	}

	personal, err = service.UpdatePersonalSubscription(ctx, actor, personal.ID, UpdatePersonalSubscriptionInput{ShadowrocketTemplateIDSet: true})
	if err != nil || personal.ShadowrocketTemplateID != nil || personal.MihomoTemplateID == nil || *personal.MihomoTemplateID != mihomo.ID {
		t.Fatal("personal templates coupled", err)
	}
	if err := service.DeleteTemplate(ctx, shadowrocket.ID); !errors.Is(err, ErrTemplateReferenced) {
		t.Fatal("plan reference not protected", err)
	}
	plan, _, err = service.UpdatePlan(ctx, plan.ID, UpdatePlanInput{ShadowrocketTemplateIDSet: true})
	if err != nil || plan.ShadowrocketTemplateID != nil || plan.MihomoTemplateID == nil || *plan.MihomoTemplateID != mihomo.ID {
		t.Fatal("plan templates coupled", err)
	}
	if err := service.DeleteTemplate(ctx, shadowrocket.ID); err != nil {
		t.Fatal(err)
	}
}

func TestClientTemplateTypeAndEnabledValidation(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 100, "admin", "admin")
	ctx := t.Context()
	actor := PersonalSubscriptionActor{UserID: 100, Role: "admin"}
	mihomo, err := service.CreateTemplate(ctx, CreateSubscriptionTemplateInput{Type: TemplateTypeMihomo, Name: "M", Enabled: true, Content: "dns: {}"})
	if err != nil {
		t.Fatal(err)
	}
	shadowrocket, err := service.CreateTemplate(ctx, CreateSubscriptionTemplateInput{Type: TemplateTypeShadowrocket, Name: "S", Enabled: true, Content: builtinShadowrocketTemplate})
	if err != nil {
		t.Fatal(err)
	}
	for _, refs := range []struct{ m, s *int64 }{{&shadowrocket.ID, nil}, {nil, &mihomo.ID}} {
		if _, err := service.CreatePlan(ctx, CreatePlanInput{Name: "Invalid", MihomoTemplateID: refs.m, ShadowrocketTemplateID: refs.s}); !errors.Is(err, ErrTemplateTypeMismatch) {
			t.Fatal(err)
		}
		if _, err := service.CreatePersonalSubscription(ctx, actor, CreatePersonalSubscriptionInput{Name: "Invalid", ClientName: "default", MihomoTemplateID: refs.m, ShadowrocketTemplateID: refs.s}); !errors.Is(err, ErrTemplateTypeMismatch) {
			t.Fatal(err)
		}
	}
	changedType := TemplateTypeShadowrocket
	if _, err := service.UpdateTemplate(ctx, mihomo.ID, UpdateSubscriptionTemplateInput{Type: &changedType}); !errors.Is(err, ErrTemplateTypeMismatch) {
		t.Fatal("template type changed", err)
	}
	content := "[Proxy]\n{{PROXIES}}"
	if _, err := service.UpdateTemplate(ctx, shadowrocket.ID, UpdateSubscriptionTemplateInput{Content: &content}); !errors.Is(err, ErrInvalidShadowrocketTemplate) {
		t.Fatal(err)
	}
	if stored, err := service.GetTemplate(ctx, shadowrocket.ID); err != nil || stored.Content != strings.TrimSpace(builtinShadowrocketTemplate) {
		t.Fatal("failed update modified template", err)
	}
	for _, kind := range []string{"", "unknown"} {
		if _, err := service.CreateTemplate(ctx, CreateSubscriptionTemplateInput{Type: kind, Name: "Invalid", Content: "dns: {}"}); !errors.Is(err, ErrTemplateTypeMismatch) {
			t.Fatal(err)
		}
	}
}
