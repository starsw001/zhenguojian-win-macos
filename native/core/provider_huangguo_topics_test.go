package core

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestHuangguoTopicsFollowWebsiteAggregation(t *testing.T) {
	document, err := html.Parse(strings.NewReader(`<div class="hg-topic-grid">
<a class="hg-topic-card" href="/topics/hot-aiduanju/"><h3 class="hg-topic-card__title">精品高分AI短剧专题</h3></a>
<a class="hg-topic-card" href="/topics/xiaoyuan%E2%80%94qingcun/"><h3 class="hg-topic-card__title">校园青春AI成人短剧</h3></a>
<a class="hg-topic-card" href="/topics/hot-aiduanju/"><h3 class="hg-topic-card__title">重复专题</h3></a>
<a class="hg-topic-card" href="https://other.example/topics/foreign/"><h3 class="hg-topic-card__title">异站专题</h3></a>
</div><a class="hg-topic-card" href="/topics/outside/"><h3 class="hg-topic-card__title">聚合外条目</h3></a>`))
	if err != nil {
		t.Fatal(err)
	}
	topics := parseHuangguoAITopics(document, "https://fixture.example/topics/")
	if len(topics) != 2 || topics[0].ID != "topic:hot-aiduanju" || topics[0].Name != "精品高分AI短剧专题" || topics[1].ID != "topic:xiaoyuan%E2%80%94qingcun" || topics[1].Kind != "topic" {
		t.Fatal(topics)
	}
	for _, category := range []string{"topic:", "topic:../other", "topic:two/parts", "topic:%2F", "topic:x?y", "topic:x\n"} {
		if validNativeCategory(sourceHuangguoAI, category) {
			t.Fatal("unsafe topic category", category)
		}
	}
	path, _, valid := huangguoAIWebsiteCategory(topics[1].ID)
	if !valid || path != "/topics/xiaoyuan%E2%80%94qingcun/" {
		t.Fatal(path, valid)
	}
}
