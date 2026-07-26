# Retriever SDK

`retriever` 提供基于 Milvus 的数据写入与检索能力，当前包含稠密向量检索、BM25 全文检索和 Vector + BM25 混合检索三种实现。

所有实现都支持 Collection 初始化、Partition 管理、批量写入、过滤检索、分页、删除、Upsert 和自定义 JSON 字段。`retriever/aisearch` 仍在开发中，当前没有可用实现。

## 功能概览

- `vector`：通过 `embedder.Embedder` 生成稠密向量并进行 ANN 检索。
- `bm25`：使用 Milvus BM25 Function 和 Sparse Vector 进行关键词检索。
- `hybrid`：同时执行向量与 BM25 检索，并使用 RRF 或 Weighted Reranker 融合结果。
- 支持 Query、Vector、BM25 和 Hybrid 搜索模式。
- 支持 Milvus Partition 的创建、加载、释放和删除。
- 支持固定的 `fields` JSON 列及 JSON Path 索引。
- 支持 Tag、标量字段和 JSON 字段过滤表达式。

## 目录结构

```text
retriever/
├── aisearch/               # 预留模块，仍在开发中
└── milvus/
    ├── config.go           # Milvus 连接配置
    ├── model.go            # Element、Retrieval、SearchArgs 和 Fields
    ├── filter.go           # 过滤表达式辅助函数
    ├── fields_index.go     # fields JSON Path 索引
    ├── vector/             # 稠密向量检索器
    ├── bm25/               # BM25 检索器
    └── hybrid/             # Vector + BM25 混合检索器
```

## 安装与前置条件

```bash
go get github.com/torrischen/goat/retriever/...
```

运行前需要：

- 一个可访问的 Milvus 2.6 服务；
- Vector 和 Hybrid 模式需要实现 `embedder.Embedder`；
- Embedder 输出维度必须与 Retriever 的 `Dimension` 完全一致；
- 如果使用 GPU 索引，需要 Milvus 环境支持对应索引类型。

仓库中的 Milvus 部署参考位于 `thirdparty/vectordb/docker-compose.yml` 和 `thirdparty/vectordb/milvus.yaml`。

## 选择检索器

| 实现 | 适用场景 | 是否需要 Embedder | 存储字段 |
| --- | --- | --- | --- |
| `vector` | 语义相似度、自然语言召回 | 是 | `id`、`tag`、`embedding`、`fields` |
| `bm25` | 关键词、专有名词、精确文本召回 | 否 | `id`、`tag`、`content`、`sparse`、`fields` |
| `hybrid` | 同时兼顾语义与关键词召回 | 是 | `id`、`tag`、`text`、`embedding`、`sparse`、`fields` |

## 快速开始：Hybrid Retriever

下面示例连接 Milvus、创建 Hybrid Retriever、写入数据并执行混合检索。

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/torrischen/goat/embedder/openai"
	"github.com/torrischen/goat/retriever/milvus"
	"github.com/torrischen/goat/retriever/milvus/hybrid"
)

func main() {
	ctx := context.Background()

	embedder := openai.NewOpenAIEmbedder(ctx, &openai.OpenAIConfig{
		BaseURL: "https://api.openai.com/v1",
		ApiKey:  os.Getenv("OPENAI_API_KEY"),
		Model:   "text-embedding-3-small",
		Dim:     1536,
	})

	retriever, err := hybrid.NewMilvusHybridRetrieverWithConfig(
		ctx,
		embedder,
		hybrid.NewHybridRetrieverConfig(
			hybrid.WithRetrieverName("documents"),
			hybrid.WithDimension(1536),
			hybrid.WithLanguage(hybrid.BM25LanguageChinese),
			hybrid.WithOnGPU(false),
			hybrid.WithFieldsIndexes(
				milvus.NewFieldsIndex("category", milvus.JSONFieldCastVarchar),
			),
		),
		milvus.NewMilvusConfig(
			milvus.WithMilvusAddress("http://localhost:19530"),
		),
	)
	if err != nil {
		log.Fatal(err)
	}

	const partition = "knowledge"
	if err := retriever.AddPartitions(ctx, partition); err != nil {
		log.Fatal(err)
	}
	if err := retriever.LoadPartitions(ctx, partition); err != nil {
		log.Fatal(err)
	}

	_, err = retriever.AddElement(ctx, partition, milvus.NewElement(
		1,
		"goat 是一个面向 Go 的 Agent 与检索工具包。",
		[]string{"goat", "golang"},
		milvus.NewFieldsFromJSONString(`{"category":"documentation","year":2026}`),
	))
	if err != nil {
		log.Fatal(err)
	}

	results, err := retriever.Search(ctx, []string{partition}, &milvus.SearchArgs{
		Text:       "Go Agent SDK",
		Limit:      10,
		SearchMode: milvus.SearchModeHybrid,
		Filter: milvus.StringEquals(
			milvus.FieldsPath("category"),
			"documentation",
		),
	})
	if err != nil {
		log.Fatal(err)
	}

	for _, result := range results {
		fmt.Printf("id=%d score=%f text=%q fields=%s\n",
			result.ID,
			result.Distance,
			result.Content,
			result.Fields.ToJSONString(),
		)
	}
}
```

构造函数会按配置创建或复用 Collection。示例固定创建 `knowledge` Partition；如果 Partition 已存在，应先通过 `HasPartition` 判断，避免重复创建错误。

## Milvus 连接

```go
config := milvus.NewMilvusConfig(
	milvus.WithMilvusAddress("http://localhost:19530"),
	milvus.WithMilvusUsername("username"),
	milvus.WithMilvusPassword("password"),
)

client, err := milvus.NewMilvusClient(ctx, config)
```

默认地址为 `http://localhost:19530`。如果多个 Retriever 共用一个连接，可先创建 `*milvusclient.Client`，再使用各实现的 `WithMilvus` 构造函数。

```go
vectorRetriever, err := vector.NewMilvusRetrieverWithMilvus(ctx, client, embedder, vectorConfig)
bm25Retriever, err := bm25.NewMilvusBM25RetrieverWithMilvus(ctx, client, bm25Config)
hybridRetriever, err := hybrid.NewMilvusHybridRetrieverWithMilvus(ctx, client, embedder, hybridConfig)
```

使用共享 Client 时由调用方负责在不再使用后关闭连接。

## 数据模型

### Element

`Element` 是写入 Retriever 的统一数据结构：

```go
element := milvus.NewElement(
	1001,
	"需要写入并用于检索的文本",
	[]string{"manual", "golang"},
	milvus.NewFieldsFromJSONString(`{"author":"team-a","year":2026}`),
)
```

| 字段 | 说明 |
| --- | --- |
| `ID` | 调用方提供的 Int64 主键。 |
| `TextToEmbed` | Vector 模式用于生成 Embedding；BM25 模式作为全文内容；Hybrid 模式同时用于两者。 |
| `Tag` | 字符串数组，可用于分类和过滤。 |
| `Fields` | 保存到固定 `fields` JSON 列的自定义数据。 |

也可以通过 `SetField` 和 `GetField` 操作单个自定义字段。

### Retrieval

```go
type Retrieval struct {
	ID       int64
	Tag      []string
	Distance float32
	Content  string
	Fields   Fields
}
```

- Vector、BM25 和 Hybrid 检索结果的分值位于 `Distance`。
- BM25 与 Hybrid 结果会在 `Content` 中返回原始文本。
- 纯 Vector Collection 不保存原始文本，因此 `Content` 为空。
- Query 模式不进行相似度计算，`Distance` 不代表检索分值。

`Retrievals` 提供 `Len`、`Index`、`Max` 和 `Min` 辅助方法。检索结果由 Milvus 按相关性顺序返回，`Max` 返回首项，`Min` 返回末项。

## Fields JSON

所有自定义字段都存储在固定的 `fields` JSON 列中。

```go
fields := milvus.NewFields()
fields.Set("author", "team-a")
fields.Set("year", 2026)

author := fields.Get("author")
rawJSON := fields.ToJSONString()

fromStruct := milvus.NewFieldsFromObject(struct {
	Category string `json:"category"`
}{Category: "guide"})
```

`Fields` 中的值应当可以被 JSON 序列化。构造函数序列化失败时会记录日志并返回 `nil`，调用方应避免传入 Channel、函数等不支持的类型。

### JSON Path

通过 `FieldsPath` 构造指向 `fields` 列的 Milvus JSON Path：

```go
milvus.FieldsPath("year")             // fields["year"]
milvus.FieldsPath("metadata", "lang") // fields["metadata"]["lang"]
```

字段名只能使用当前索引实现支持的安全名称。不要直接拼接来自用户输入的字段路径。

### JSON 字段索引

可以在 Retriever 初始化时声明索引：

```go
milvus.NewFieldsIndex("category", milvus.JSONFieldCastVarchar)
milvus.NewFieldsPathIndex(
	milvus.FieldsPath("metadata", "year"),
	milvus.JSONFieldCastDouble,
)
```

支持的 Cast 类型：

- `JSONFieldCastBool`
- `JSONFieldCastDouble`
- `JSONFieldCastVarchar`

Vector 使用 `WithAutoIndexFields(true)`，BM25 和 Hybrid 使用 `WithFieldsAutoIndex(true)`，可在写入数据时根据 `Fields` 自动发现并创建索引。生产环境更建议通过 `WithFieldsIndexes` 显式声明，避免数据类型变化导致意外索引。

## 过滤表达式

### 标量过滤

```go
filter := milvus.IntGreaterThan(milvus.FieldsPath("year"), 2024)
filter = milvus.StringEquals(milvus.FieldsPath("status"), "published")
filter = milvus.StringLike(milvus.FieldsPath("title"), "%goat%")
```

### 集合过滤

```go
filter := milvus.IntIn(milvus.FieldsPath("category_id"), []int64{1, 2, 3})
filter = milvus.StringIn(milvus.FieldsPath("lang"), []string{"zh", "en"})
filter = milvus.ArrayContainsAny(milvus.ColumnTag, []string{"golang", "agent"})
```

### 组合过滤

```go
filter := milvus.And([]milvus.RetrieveFilterOption{
	milvus.IntGreaterThan(milvus.FieldsPath("year"), 2024),
	milvus.ArrayContainsAny(milvus.ColumnTag, []string{"documentation"}),
})

filter = milvus.Or([]milvus.RetrieveFilterOption{
	milvus.StringEquals(milvus.FieldsPath("status"), "published"),
	milvus.StringEquals(milvus.FieldsPath("status"), "featured"),
})
```

`RetrieveFilterOption` 本质上是 Milvus 表达式字符串。辅助函数会处理字符串字面量转义，但字段路径仍应由可信代码控制。

## 搜索参数与模式

```go
type SearchArgs struct {
	Text          string
	Limit         int
	Offset        int
	Filter        RetrieveFilterOption
	OutputFields  []string
	SearchMode    SearchMode
	RerankWeights []float64
}
```

| 字段 | 说明 |
| --- | --- |
| `Text` | 用于 Embedding 或 BM25 的查询文本。 |
| `Limit` | 返回条数；非正数时内部搜索默认使用 `8`。 |
| `Offset` | 分页偏移量。 |
| `Filter` | Milvus 过滤表达式；零值表示不过滤。 |
| `OutputFields` | 需要从 `fields` JSON 中返回的字段名。 |
| `SearchMode` | Query、Vector、BM25、Hybrid 或 Auto。 |
| `RerankWeights` | Hybrid 模式的 Weighted Reranker 权重；为空时使用 RRF。 |

可用模式：

- `SearchModeQuery`：只按 Filter 查询，不计算相似度。
- `SearchModeVector`：使用稠密向量搜索。
- `SearchModeBM25`：使用全文 BM25 搜索。
- `SearchModeHybrid`：融合 Vector 与 BM25 结果。
- `SearchModeAuto`：使用当前 Retriever 的默认搜索模式。

当 `SearchModeAuto` 且 `Text` 非空时，Vector Retriever 默认使用 Vector、BM25 Retriever 默认使用 BM25、Hybrid Retriever 默认使用 Hybrid；当参数为空或 `Text` 为空时，Auto 会退化为 Query。各 Retriever 只支持与自身 Collection Schema 兼容的模式。

`SimilaritySearch` 是对 `Search` 的兼容入口，推荐新代码直接使用 `Search` 以显式选择模式。

## Vector Retriever

### 配置

```go
config := vector.NewMilvusRetrieverConfig(
	vector.WithRetrieverName("vector_documents"),
	vector.WithDimension(1536),
	vector.WithShardNum(2),
	vector.WithOverwrite(false),
	vector.WithVariableTags(true),
	vector.WithOnGPU(false),
	vector.WithFieldsIndexes(
		milvus.NewFieldsIndex("category", milvus.JSONFieldCastVarchar),
	),
	vector.WithAutoIndexFields(false),
)
```

主要默认值：Collection 名为 `default_collection`、维度 `512`、不覆盖已有 Collection、GPU 索引开启、Fields 自动索引关闭。

### 构造

```go
retriever, err := vector.NewMilvusRetrieverWithConfig(ctx, embedder, config, milvusConfig)
```

Vector Retriever 额外提供 `Query`，可以读取 ID、Tag、Embedding 和 Fields 原始数据；通用结果检索优先使用 `Search`。

## BM25 Retriever

BM25 Retriever 不需要 Embedder：

```go
config := bm25.NewBM25RetrieverConfig(
	bm25.WithRetrieverName("keyword_documents"),
	bm25.WithLanguage(bm25.BM25LanguageChinese),
	bm25.WithMaxTextLength(4096),
	bm25.WithDropRatio(0.2),
	bm25.WithOverwrite(false),
	bm25.WithFieldsAutoIndex(false),
)

retriever, err := bm25.NewMilvusBM25RetrieverWithConfig(ctx, config, milvusConfig)
```

可用语言包括 English、Chinese、Japanese 和 Korean。当前默认语言为 Japanese，中文数据应显式设置 `BM25LanguageChinese`。

## Hybrid Retriever

```go
config := hybrid.NewHybridRetrieverConfig(
	hybrid.WithRetrieverName("hybrid_documents"),
	hybrid.WithDimension(1536),
	hybrid.WithLanguage(hybrid.BM25LanguageChinese),
	hybrid.WithOnGPU(false),
)

retriever, err := hybrid.NewMilvusHybridRetrieverWithConfig(
	ctx,
	embedder,
	config,
	milvusConfig,
)
```

Hybrid 模式默认使用 RRF 融合结果。需要控制向量和关键词权重时：

```go
results, err := retriever.Search(ctx, partitions, &milvus.SearchArgs{
	Text:          "query text",
	Limit:         10,
	SearchMode:    milvus.SearchModeHybrid,
	RerankWeights: []float64{0.8, 0.2}, // vector, BM25
})
```

权重顺序与内部请求顺序一致：第一项是 Vector，第二项是 BM25。

## Partition 管理

三种 Retriever 提供一致的 Partition API：

```go
exists, err := retriever.HasPartition(ctx, "tenant_a")
if !exists {
	err = retriever.AddPartitions(ctx, "tenant_a")
}

err = retriever.LoadPartitions(ctx, "tenant_a")
partitions, err := retriever.ListPartitions(ctx)
err = retriever.ReleasePartitions(ctx, "tenant_a")
err = retriever.DeletePartitions(ctx, "tenant_a")
```

- 写入前应确保目标 Partition 已创建；检索前还应确保目标 Partition 已加载。
- `milvus.DefaultPartition` 的值为 `_default`。
- 删除 Partition 前应先释放，并确认没有仍需保留的数据。
- Partition 适合租户或数据域隔离，不建议为每条数据创建独立 Partition。

## 写入、更新与删除

```go
id, err := retriever.AddElement(ctx, partition, element)

ids, err := retriever.AddElements(ctx, partition, []*milvus.Element{
	elementA,
	elementB,
})

err = retriever.UpsertElement(ctx, partition, updatedElement)
err = retriever.DeleteElement(ctx, partition, []int64{1001, 1002})
```

建议使用 `AddElements` 批量写入以减少网络往返。调用方负责保证同一批次 ID 和数据内容符合业务约束。

## Collection 生命周期

所有 Retriever Config 都支持 `WithOverwrite(true)`。启用后构造 Retriever 可能删除并重建同名 Collection，会造成数据丢失，只应在测试、初始化或明确重建索引时使用。

也可以显式销毁 Collection：

```go
err := vector.TruncateAndDestroy(ctx, client, "vector_documents")
err := bm25.TruncateAndDestroy(ctx, client, "keyword_documents")
err := hybrid.TruncateAndDestroy(ctx, client, "hybrid_documents")
```

这些函数是破坏性操作，生产环境调用前必须增加权限控制和二次确认。

## 最佳实践

- Retriever 的 `Dimension` 必须与 Embedder 实际输出维度一致。
- 中文 BM25 或 Hybrid 数据显式设置 Chinese Analyzer，不要依赖默认语言。
- 生产环境默认使用 `WithOverwrite(false)`。
- 先声明常用 JSON 字段索引，再对这些字段执行高频过滤。
- 避免在同一 JSON Path 中混用字符串、数字和布尔类型。
- 使用稳定、唯一的 Int64 ID，以便安全 Upsert 和删除。
- 批量写入后，根据业务一致性要求等待 Milvus 数据可见再立即检索。
- 使用 Context Timeout 限制连接、索引创建、加载和检索耗时。
- 不要把未经校验的用户输入直接作为字段路径或原始 Milvus Filter。

## 测试与编译检查

Retriever 当前依赖外部 Milvus 服务，仓库内主要通过编译检查验证：

```bash
go test ./retriever/...
```

端到端验证还应覆盖：

1. 创建独立测试 Collection；
2. 创建并加载 Partition；
3. 写入固定测试数据；
4. 分别执行 Query、Vector、BM25 和 Hybrid 检索；
5. 验证 Filter、Fields 和排序结果；
6. 删除测试 Collection。
