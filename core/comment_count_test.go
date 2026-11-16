package core

import "testing"

func TestCommentCountMatchesVisibleThreads(t *testing.T) {
	c, r := setup(t)
	a := addC(t, c, r, "/a", sel(doc, "один"), "первый")
	b := addC(t, c, r, "/a", sel(doc, "два"), "второй")
	q := addC(t, c, r, "/a", sel(doc, "три"), "третий")
	sendTake(t, c, r)
	_, err := c.AgentFinish(r, nil, []Answer{{CommentID: a, Text: "сделано"}, {CommentID: b, Text: "сделано"}, {CommentID: q, Text: "уточните", Question: true}})
	must(t, err)
	if v := fv(t, c, r, "/a"); v.Open != 3 || v.Questions != 1 {
		t.Fatalf("до просмотра: %+v", v)
	}
	must(t, c.AcceptResponse(r))
	if v := fv(t, c, r, "/a"); v.Open != 1 || v.Questions != 1 {
		t.Fatalf("после просмотра: %+v", v)
	}
	must(t, c.DismissQuestion(r, q))
	if v := fv(t, c, r, "/a"); v.Open != 0 || v.Questions != 0 {
		t.Fatalf("только скрытые ветки: %+v", v)
	}
}
