package core

import "testing"

// TestGuipianExplicitTerminalOverridesBrokenNextLink 验证「Guipian Explicit Terminal Overrides Broken Next Link」回归场景。
func TestGuipianExplicitTerminalOverridesBrokenNextLink(t *testing.T) {
	body := `<ul class="ewave-page text-center clearfix"><li><span class="num">3/3</span></li><li><a href="/list/26_4.html">下一页</a></li><li><a href="/list/26_3.html">尾页</a></li></ul>`
	for _, page := range []int{3, 4, 5} {
		if last := guipianPageCount(body, "26", page, 24); last != 3 || page < last {
			t.Fatalf("a full terminal page exposed an invented continuation: page=%d last=%d", page, last)
		}
	}
}

// TestGuipianPageTotalIsScopedAndValidated 验证「Guipian Page Total Is Scoped And Validated」回归场景。
func TestGuipianPageTotalIsScopedAndValidated(t *testing.T) {
	for _, sample := range []struct {
		body string
		want int
	}{
		{`<span class="num">3/3</span><a href="/list/26_8.html">8</a>`, 8},
		{`<ul class="ewave-page"><span class="num">0/3</span><a href="/list/26_8.html">8</a></ul>`, 8},
		{`<ul class="ewave-page"><span class="num">4/3</span><a href="/list/26_8.html">8</a></ul>`, 8},
		{`<ul class="ewave-page"><span class="num">1/99999999999999999999999</span></ul>`, 4},
		{`<ul class='ewave-page extra'><span class='other num'> 3 / 8 </span></ul>`, 8},
	} {
		if got := guipianPageCount(sample.body, "26", 3, 24); got != sample.want {
			t.Fatalf("unexpected terminal page: got=%d want=%d", got, sample.want)
		}
	}
}
