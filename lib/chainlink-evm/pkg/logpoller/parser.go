package logpoller

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	evmprimitives "github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives/evm"
	evmtypes "github.com/smartcontractkit/chainlink-evm/pkg/types"
)

const (
	blockFieldName     = "block_number"
	timestampFieldName = "block_timestamp"
	txHashFieldName    = "tx_hash"
	eventSigFieldName  = "event_sig"
	defaultSort        = "block_number DESC, log_index DESC"
)

var (
	ErrUnexpectedCursorFormat = errors.New("unexpected cursor format")
	logsFields                = [...]string{"evm_chain_id", "log_index", "block_hash", "block_number",
		"address", "event_sig", "topics", "tx_hash", "data", "created_at", "block_timestamp"}
	blocksFields = [...]string{"evm_chain_id", "block_hash", "block_number", "block_timestamp",
		"finalized_block_number", "created_at", "safe_block_number"}
)

// The parser builds SQL expressions piece by piece for each Accept function call and resets the error and expression
// values after every call.
type pgDSLParser struct {
	args *queryArgs

	// transient properties expected to be set and reset with every expression
	expression string
	err        error
}

var _ primitives.Visitor = (*pgDSLParser)(nil)
var _ evmprimitives.Visitor = (*pgDSLParser)(nil)

func (v *pgDSLParser) Comparator(_ primitives.Comparator) {}

func (v *pgDSLParser) Block(p primitives.Block) {
	cmp, err := cmpOpToString(p.Operator)
	if err != nil {
		v.err = err

		return
	}

	v.expression = fmt.Sprintf(
		"%s %s :%s",
		blockFieldName,
		cmp,
		v.args.withIndexedField(blockFieldName, p.Block),
	)
}

func (v *pgDSLParser) Confidence(p primitives.Confidence) {
	switch p.ConfidenceLevel {
	case primitives.Finalized, primitives.Unconfirmed, primitives.Safe:
		v.expression = v.nestedConfQuery(p.ConfidenceLevel, 0)
	default:
		v.err = errors.New("unrecognized confidence level; use confidence to confirmations mappings instead")
		return
	}
}

func (v *pgDSLParser) Timestamp(p primitives.Timestamp) {
	cmp, err := cmpOpToString(p.Operator)
	if err != nil {
		v.err = err

		return
	}

	v.expression = fmt.Sprintf(
		"%s %s :%s",
		timestampFieldName,
		cmp,
		v.args.withIndexedField(timestampFieldName, time.Unix(int64(p.Timestamp), 0)),
	)
}

func (v *pgDSLParser) TxHash(p primitives.TxHash) {
	bts, err := hexutil.Decode(p.TxHash)
	if errors.Is(err, hexutil.ErrMissingPrefix) {
		bts, err = hexutil.Decode("0x" + p.TxHash)
	}

	if err != nil {
		v.err = err

		return
	}

	txHash := common.BytesToHash(bts)

	v.expression = fmt.Sprintf(
		"%s = :%s",
		txHashFieldName,
		v.args.withIndexedField(txHashFieldName, txHash),
	)
}

func (v *pgDSLParser) visitAddressFilter(p *addressFilter) {
	v.expression = "address = :" + v.args.withIndexedField("address", p.address)
}

func (v *pgDSLParser) visitEventSigFilter(p *eventSigFilter) {
	v.expression = fmt.Sprintf(
		"%s = :%s",
		eventSigFieldName,
		v.args.withIndexedField(eventSigFieldName, p.eventSig),
	)
}

func (v *pgDSLParser) nestedConfQuery(confidenceLevel primitives.ConfidenceLevel, confs uint64) string {
	var (
		from     = "FROM evm.log_poller_blocks "
		where    = "WHERE evm_chain_id = :evm_chain_id "
		order    = "ORDER BY block_number DESC LIMIT 1"
		selector string
	)

	switch confidenceLevel {
	case primitives.Finalized:
		selector = "SELECT finalized_block_number "
	case primitives.Safe:
		selector = "SELECT safe_block_number "
	default: // primitives.Unconfirmed scenario, as we won't fail in this function, it will be the default case
		selector = fmt.Sprintf("SELECT greatest(block_number - :%s, 0) ",
			v.args.withIndexedField("confs", confs),
		)
	}

	var builder strings.Builder

	builder.WriteString(selector)
	builder.WriteString(from)
	builder.WriteString(where)
	builder.WriteString(order)

	return fmt.Sprintf("%s <= (%s)", blockFieldName, builder.String())
}

func (v *pgDSLParser) visitEventByWordFilter(p *eventByWordFilter) {
	if len(p.HashedValueComparers) > 0 {
		columnName := fmt.Sprintf("substring(data from 32*%d+1 for 32)", p.WordIndex)

		comps := make([]string, len(p.HashedValueComparers))
		for idx, comp := range p.HashedValueComparers {
			comps[idx], v.err = v.hashedValueCmpToCondition(comp, columnName, "word_value")
			if v.err != nil {
				return
			}
		}

		v.expression = strings.Join(comps, " AND ")
	}
}
func (v *pgDSLParser) visitEventTopicsByValueFilter(p *eventByTopicFilter) {
	if len(p.ValueComparers) == 0 {
		return
	}

	if !(p.Topic == 1 || p.Topic == 2 || p.Topic == 3) {
		v.err = fmt.Errorf("invalid index for topic: %d", p.Topic)

		return
	}

	// Add 1 since postgresql arrays are 1-indexed.
	columnName := fmt.Sprintf("topics[%d]", p.Topic+1)

	comps := make([]string, len(p.ValueComparers))
	for idx, comp := range p.ValueComparers {
		comps[idx], v.err = v.hashedValueCmpToCondition(comp, columnName, "topic_value")
		if v.err != nil {
			return
		}
	}

	v.expression = strings.Join(comps, " AND ")
}

func (v *pgDSLParser) Address(f *evmprimitives.Address) {
	v.visitAddressFilter(toAddress(f))
}

func (v *pgDSLParser) EventSig(f *evmprimitives.EventSig) {
	v.visitEventSigFilter(toEventSig(f))
}

func (v *pgDSLParser) EventTopicsByValue(f *evmprimitives.EventByTopic) {
	v.visitEventTopicsByValueFilter(toEventTopicsByValue(f))
}

func (v *pgDSLParser) EventByWord(f *evmprimitives.EventByWord) {
	v.visitEventByWordFilter(toEventByWord(f))
}

func (v *pgDSLParser) VisitConfirmationsFilter(p *confirmationsFilter) {
	switch p.Confirmations {
	case evmtypes.Finalized:
		// the highest level of confidence maps to finalized
		v.expression = v.nestedConfQuery(primitives.Finalized, 0)
	case evmtypes.Safe:
		v.expression = v.nestedConfQuery(primitives.Safe, 0)
	default:
		v.expression = v.nestedConfQuery(primitives.Unconfirmed, uint64(p.Confirmations)) //nolint:gosec // G115
	}
}

func (v *pgDSLParser) hashedValueCmpToCondition(comp HashedValueComparator, column, fieldName string) (string, error) {
	cmp, err := cmpOpToString(comp.Operator)
	if err != nil {
		return "", err
	}

	// simplify query for Postgres as in some cases, it's not that smart
	if len(comp.Values) == 1 {
		return fmt.Sprintf("%s %s :%s", column, cmp, v.args.withIndexedField(fieldName, comp.Values[0])), nil
	}

	return fmt.Sprintf("%s %s ANY(:%s)", column, cmp, v.args.withIndexedField(fieldName, comp.Values)), nil
}

func (v *pgDSLParser) buildQuery(chainID *big.Int, expressions []query.Expression, limiter query.LimitAndSort) (string, *queryArgs, error) {
	// reset transient properties
	v.args = newQueryArgs(chainID)
	v.expression = ""
	v.err = nil

	// build the query string
	clauses := []string{logsQuery("")}

	where, err := v.whereClause(expressions, limiter)
	if err != nil {
		return "", nil, err
	}

	clauses = append(clauses, where)

	order, err := v.orderClause(limiter)
	if err != nil {
		return "", nil, err
	}

	if len(order) > 0 {
		clauses = append(clauses, order)
	}

	limit := v.limitClause(limiter)
	if len(limit) > 0 {
		clauses = append(clauses, limit)
	}

	return strings.Join(clauses, " "), v.args, nil
}

func (v *pgDSLParser) whereClause(expressions []query.Expression, limiter query.LimitAndSort) (string, error) {
	segment := "WHERE evm_chain_id = :evm_chain_id"

	if len(expressions) > 0 {
		exp, hasFinalized, err := v.combineExpressions(expressions, query.AND)
		if err != nil {
			return "", err
		}

		if limiter.HasCursorLimit() && !hasFinalized {
			return "", errors.New("cursor-base queries limited to only finalized blocks")
		}

		segment = fmt.Sprintf("%s AND %s", segment, exp)
	}

	if limiter.HasCursorLimit() {
		var op string
		switch limiter.Limit.CursorDirection {
		case query.CursorFollowing:
			op = ">"
		case query.CursorPrevious:
			op = "<"
		default:
			return "", errors.New("invalid cursor direction")
		}

		block, logIdx, _, err := valuesFromCursor(limiter.Limit.Cursor)
		if err != nil {
			return "", err
		}

		segment = fmt.Sprintf("%s AND (block_number %s :cursor_block_number OR (block_number = :cursor_block_number AND log_index %s :cursor_log_index))", segment, op, op)

		v.args.withField("cursor_block_number", block).
			withField("cursor_log_index", logIdx)
	}

	return segment, nil
}

func (v *pgDSLParser) orderClause(limiter query.LimitAndSort) (string, error) {
	sorting := limiter.SortBy

	if limiter.HasCursorLimit() && !limiter.HasSequenceSort() {
		var dir query.SortDirection

		switch limiter.Limit.CursorDirection {
		case query.CursorFollowing:
			dir = query.Asc
		case query.CursorPrevious:
			dir = query.Desc
		default:
			return "", errors.New("unexpected cursor direction")
		}

		sorting = append(sorting, query.NewSortBySequence(dir))
	}

	if len(sorting) == 0 {
		return "ORDER BY " + defaultSort, nil
	}

	sort := make([]string, len(sorting))

	for idx, sorted := range sorting {
		var name string

		order, err := orderToString(sorted.GetDirection())
		if err != nil {
			return "", err
		}

		switch sorted.(type) {
		case query.SortByBlock:
			name = blockFieldName
		case query.SortBySequence:
			sort[idx] = fmt.Sprintf("block_number %s, log_index %s, tx_hash %s", order, order, order)

			continue
		case query.SortByTimestamp:
			name = timestampFieldName
		default:
			return "", errors.New("unexpected sort by")
		}

		sort[idx] = fmt.Sprintf("%s %s", name, order)
	}

	return "ORDER BY " + strings.Join(sort, ", "), nil
}

func (v *pgDSLParser) limitClause(limiter query.LimitAndSort) string {
	if !limiter.HasCursorLimit() && limiter.Limit.Count == 0 {
		return ""
	}

	return fmt.Sprintf("LIMIT %d", limiter.Limit.Count)
}

func (v *pgDSLParser) getLastExpression() (string, error) {
	exp := v.expression
	err := v.err

	v.expression = ""
	v.err = nil

	return exp, err
}

func (v *pgDSLParser) combineExpressions(expressions []query.Expression, op query.BoolOperator) (string, bool, error) {
	grouped := len(expressions) > 1
	clauses := make([]string, len(expressions))

	var isFinalized bool

	for idx, exp := range expressions {
		if exp.IsPrimitive() {
			exp.Primitive.Accept(v)

			switch prim := exp.Primitive.(type) {
			case *primitives.Confidence:
				isFinalized = prim.ConfidenceLevel == primitives.Finalized
			case *confirmationsFilter:
				isFinalized = prim.Confirmations == evmtypes.Finalized
			}

			clause, err := v.getLastExpression()
			if err != nil {
				return "", isFinalized, err
			}

			clauses[idx] = clause
		} else {
			clause, fin, err := v.combineExpressions(exp.BoolExpression.Expressions, exp.BoolExpression.BoolOperator)
			if err != nil {
				return "", isFinalized, err
			}

			if fin {
				isFinalized = fin
			}

			clauses[idx] = clause
		}
	}

	output := strings.Join(clauses, fmt.Sprintf(" %s ", op.String()))

	if grouped {
		output = fmt.Sprintf("(%s)", output)
	}

	return output, isFinalized, nil
}

func cmpOpToString(op primitives.ComparisonOperator) (string, error) {
	switch op {
	case primitives.Eq:
		return "=", nil
	case primitives.Neq:
		return "!=", nil
	case primitives.Gt:
		return ">", nil
	case primitives.Gte:
		return ">=", nil
	case primitives.Lt:
		return "<", nil
	case primitives.Lte:
		return "<=", nil
	default:
		return "", errors.New("invalid comparison operator")
	}
}

func orderToString(dir query.SortDirection) (string, error) {
	switch dir {
	case query.Asc:
		return "ASC", nil
	case query.Desc:
		return "DESC", nil
	default:
		return "", errors.New("invalid sort direction")
	}
}

// MakeContractReaderCursor is exported to ensure cursor structure remains consistent.
func FormatContractReaderCursor(log Log) string {
	return fmt.Sprintf("%d-%d-%s", log.BlockNumber, log.LogIndex, log.TxHash)
}

// ensure valuesFromCursor remains consistent with the function above that creates a cursor
func valuesFromCursor(cursor string) (int64, int, []byte, error) {
	partCount := 3

	parts := strings.Split(cursor, "-")
	if len(parts) != partCount {
		return 0, 0, nil, fmt.Errorf("%w: must be composed as block-logindex-txHash", ErrUnexpectedCursorFormat)
	}

	block, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("%w: block number not parsable as int64", ErrUnexpectedCursorFormat)
	}

	logIdx, err := strconv.ParseInt(parts[1], 10, 32)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("%w: log index not parsable as int", ErrUnexpectedCursorFormat)
	}

	txHash, err := hexutil.Decode(parts[2])
	if err != nil {
		return 0, 0, nil, fmt.Errorf("%w: invalid transaction hash: %s", ErrUnexpectedCursorFormat, err.Error())
	}

	return block, int(logIdx), txHash, nil
}

type addressFilter struct {
	address common.Address
}

func NewAddressFilter(address common.Address) query.Expression {
	return query.Expression{
		Primitive: &addressFilter{address: address},
	}
}

func (f *addressFilter) Accept(visitor primitives.Visitor) {
	switch v := visitor.(type) {
	case *pgDSLParser:
		v.visitAddressFilter(f)
	}
}

type eventSigFilter struct {
	eventSig common.Hash
}

func NewEventSigFilter(hash common.Hash) query.Expression {
	return query.Expression{
		Primitive: &eventSigFilter{eventSig: hash},
	}
}

func (f *eventSigFilter) Accept(visitor primitives.Visitor) {
	switch v := visitor.(type) {
	case *pgDSLParser:
		v.visitEventSigFilter(f)
	}
}

type HashedValueComparator struct {
	Values   []common.Hash
	Operator primitives.ComparisonOperator
}

type eventByWordFilter struct {
	WordIndex            int
	HashedValueComparers []HashedValueComparator
}

func NewEventByWordFilter(wordIndex int, valueComparers []HashedValueComparator) query.Expression {
	return query.Expression{Primitive: &eventByWordFilter{
		WordIndex:            wordIndex,
		HashedValueComparers: valueComparers,
	}}
}

func (f *eventByWordFilter) Accept(visitor primitives.Visitor) {
	switch v := visitor.(type) {
	case *pgDSLParser:
		v.visitEventByWordFilter(f)
	}
}

type eventByTopicFilter struct {
	Topic          uint64
	ValueComparers []HashedValueComparator
}

func NewEventByTopicFilter(topicIndex uint64, valueComparers []HashedValueComparator) query.Expression {
	return query.Expression{Primitive: &eventByTopicFilter{
		Topic:          topicIndex,
		ValueComparers: valueComparers,
	}}
}

func (f *eventByTopicFilter) Accept(visitor primitives.Visitor) {
	switch v := visitor.(type) {
	case *pgDSLParser:
		v.visitEventTopicsByValueFilter(f)
	}
}

type confirmationsFilter struct {
	Confirmations evmtypes.Confirmations
}

func NewConfirmationsFilter(confirmations evmtypes.Confirmations) query.Expression {
	return query.Expression{Primitive: &confirmationsFilter{
		Confirmations: confirmations,
	}}
}

func (f *confirmationsFilter) Accept(visitor primitives.Visitor) {
	switch v := visitor.(type) {
	case *pgDSLParser:
		v.VisitConfirmationsFilter(f)
	}
}

func toAddress(f *evmprimitives.Address) *addressFilter {
	return &addressFilter{
		address: f.Address,
	}
}

func toEventSig(f *evmprimitives.EventSig) *eventSigFilter {
	return &eventSigFilter{
		eventSig: f.EventSig,
	}
}

func toEventTopicsByValue(f *evmprimitives.EventByTopic) *eventByTopicFilter {
	return &eventByTopicFilter{
		Topic:          f.Topic,
		ValueComparers: toHashValueComparers(f.HashedValueComparers),
	}
}

func toEventByWord(f *evmprimitives.EventByWord) *eventByWordFilter {
	return &eventByWordFilter{
		WordIndex:            f.WordIndex,
		HashedValueComparers: toHashValueComparers(f.HashedValueComparers),
	}
}

func toHashValueComparers(cs []evmprimitives.HashedValueComparator) []HashedValueComparator {
	ret := make([]HashedValueComparator, 0, len(cs))

	for _, c := range cs {
		ret = append(ret, HashedValueComparator{
			Values:   toHashes(c.Values),
			Operator: c.Operator,
		})
	}

	return ret
}

func toHashes(ss []evm.Hash) []common.Hash {
	ret := make([]common.Hash, 0, len(ss))
	for _, s := range ss {
		ret = append(ret, s)
	}

	return ret
}
