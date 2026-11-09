// This file adapts upstream packages/llm/llm/src/assembler.ts to the simplified
// text/reasoning/tool-call/done protocol. While the agent logs raw chunks,
// BlockAssembler is the authoritative chunk-to-message conversion: adjacent
// text or reasoning deltas merge, tool calls become blocks on arrival, and
// output preserves stream order.
package llm

// BlockAssembler incrementally converts StreamChunk values to ContentBlock values.
// Use NewBlockAssembler rather than the zero value.
type BlockAssembler struct {
	curKind  StreamChunkKind // The current delta kind; empty means no open buffer.
	curBuf   string
	blocks   []ContentBlock
	finished bool
}

// NewBlockAssembler creates an empty assembler.
func NewBlockAssembler() *BlockAssembler {
	return &BlockAssembler{}
}

// flush commits the open text or reasoning buffer as a block.
func (a *BlockAssembler) flush() {
	if a.curKind == "" {
		return
	}
	if a.curBuf != "" {
		a.blocks = append(a.blocks, ContentBlock{
			Kind: BlockReasoning,
			Text: a.curBuf,
		})
		if a.curKind == ChunkText {
			a.blocks[len(a.blocks)-1].Kind = BlockText
		}
	}
	a.curKind = ""
	a.curBuf = ""
}

// Push consumes a chunk in stream order.
func (a *BlockAssembler) Push(c StreamChunk) {
	switch c.Kind {
	case ChunkText:
		if a.curKind != ChunkText {
			a.flush()
			a.curKind = ChunkText
		}
		a.curBuf += c.Text
	case ChunkReasoning:
		if a.curKind != ChunkReasoning {
			a.flush()
			a.curKind = ChunkReasoning
		}
		a.curBuf += c.Reasoning
	case ChunkToolCall:
		a.flush()
		if c.ToolCall != nil {
			a.blocks = append(a.blocks, ToolUse(c.ToolCall))
		}
	case ChunkDone:
		a.flush()
		a.finished = true
	}
}

// Blocks returns a copy of assembled blocks in stream order, including any open buffer.
func (a *BlockAssembler) Blocks() []ContentBlock {
	a.flush()
	out := make([]ContentBlock, len(a.blocks))
	copy(out, a.blocks)
	return out
}

// Message constructs an assistant message from assembled blocks.
func (a *BlockAssembler) Message() Message {
	return Message{Role: RoleAssistant, Content: a.Blocks()}
}

// Finished reports whether a done chunk has arrived.
func (a *BlockAssembler) Finished() bool { return a.finished }
