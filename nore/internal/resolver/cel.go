package resolver

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"

	"github.com/neuron-runtime/neuron/nore/internal/data"
)

type CELConfig struct {
	MaxExpressionSize       int
	MaxRecursionDepth       int
	MaxASTNodes             int
	CostLimit               uint64
	InterruptCheckFrequency uint

	MaxTemplateSize        int
	MaxTemplateExpressions int
	MaxResolvedStringSize  int
}

func DefaultCELConfig() CELConfig {
	return CELConfig{
		MaxExpressionSize:       4_096,
		MaxRecursionDepth:       64,
		MaxASTNodes:             2_000,
		CostLimit:               10_000,
		InterruptCheckFrequency: 100,
		MaxTemplateSize:         64 * 1024,
		MaxTemplateExpressions:  128,
		MaxResolvedStringSize:   1024 * 1024,
	}
}

type celCompiler struct {
	transitionEnv *cel.Env
	capabilityEnv *cel.Env
	config        CELConfig
}

type celExpression struct {
	expression string
	program    cel.Program
}

type transitionProgram struct {
	expression *celExpression
}

func NewCELCompiler(config CELConfig) (Compiler, error) {
	config = normalizeConfig(config)

	commonOptions := []cel.EnvOption{
		cel.ParserExpressionSizeLimit(config.MaxExpressionSize),
		cel.ParserRecursionLimit(config.MaxRecursionDepth),
		cel.ExpressionNodeLimit(config.MaxASTNodes),
	}

	transitionEnv, err := cel.NewEnv(append(commonOptions,
		cel.Variable("source", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("execution", cel.MapType(cel.StringType, cel.DynType)),
	)...)
	if err != nil {
		return nil, fmt.Errorf("create transition CEL environment: %w", err)
	}

	// The declared variable names must match the activation bindings in
	// templateNode.resolve. Capability configuration templates address the
	// invocation payload as `params`, matching the canonical parameter name
	// used by Binding transition expressions and the execution engine.
	capabilityEnv, err := cel.NewEnv(append(commonOptions,
		cel.Variable("params", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("execution", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("capability", cel.MapType(cel.StringType, cel.DynType)),
	)...)
	if err != nil {
		return nil, fmt.Errorf("create capability CEL environment: %w", err)
	}

	return &celCompiler{
		transitionEnv: transitionEnv,
		capabilityEnv: capabilityEnv,
		config:        config,
	}, nil
}

func (c *celCompiler) CompileTransitionExpression(expression string) (Program, error) {
	compiled, err := c.compileExpression(c.transitionEnv, expression)
	if err != nil {
		return nil, err
	}
	return &transitionProgram{expression: compiled}, nil
}

func (c *celCompiler) CompileCapabilityConfigurations(config map[string]any) (ConfigurationProgram, error) {
	node, err := c.compileConfigurationValue(config, "config")
	if err != nil {
		return nil, err
	}
	return &configurationProgram{root: node}, nil
}

func (c *celCompiler) compileExpression(env *cel.Env, expression string) (*celExpression, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return nil, fmt.Errorf("expression cannot be empty")
	}

	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("compile expression %q: %w", expression, issues.Err())
	}

	program, err := env.Program(
		ast,
		cel.EvalOptions(cel.OptOptimize),
		cel.CostLimit(c.config.CostLimit),
		cel.InterruptCheckFrequency(c.config.InterruptCheckFrequency),
	)
	if err != nil {
		return nil, fmt.Errorf("create CEL program for %q: %w", expression, err)
	}

	return &celExpression{expression: expression, program: program}, nil
}

func (p *transitionProgram) Evaluate(ctx context.Context, environment Environment) (any, error) {
	return p.expression.evaluate(ctx, map[string]any{
		"source":    environment.Source,
		"execution": environment.Execution,
	})
}

func (p *transitionProgram) Expression() string {
	return p.expression.expression
}

func (p *celExpression) evaluate(ctx context.Context, variables map[string]any) (any, error) {
	if ctx == nil {
		return nil, fmt.Errorf("evaluation context is required")
	}

	output, _, err := p.program.ContextEval(ctx, variables)
	if err != nil {
		return nil, fmt.Errorf("evaluate expression %q: %w", p.expression, err)
	}
	if output == nil {
		return nil, nil
	}
	// CEL models JSON null as types.NullValue, whose Value() is a
	// structpb.NullValue rather than a Go nil. Letting that through silently
	// turns a null field into the number 0 on its way to a capability runtime,
	// so translate it to a real nil here, where every expression passes.
	if output == types.NullValue {
		return nil, nil
	}
	// CEL returns int64 for a computed number but the original native Go value
	// for one copied wholesale out of an activation map, so the same JSON number
	// would reach a capability as either int64 or int depending only on how the
	// expression was written. Every value leaves the resolver in the canonical
	// representation, which is also what an execution observes after a snapshot
	// restore.
	return data.CanonicalValue(output.Value()), nil
}

func normalizeConfig(config CELConfig) CELConfig {
	defaults := DefaultCELConfig()
	if config.MaxExpressionSize <= 0 {
		config.MaxExpressionSize = defaults.MaxExpressionSize
	}
	if config.MaxRecursionDepth <= 0 {
		config.MaxRecursionDepth = defaults.MaxRecursionDepth
	}
	if config.MaxASTNodes <= 0 {
		config.MaxASTNodes = defaults.MaxASTNodes
	}
	if config.CostLimit == 0 {
		config.CostLimit = defaults.CostLimit
	}
	if config.InterruptCheckFrequency == 0 {
		config.InterruptCheckFrequency = defaults.InterruptCheckFrequency
	}
	if config.MaxTemplateSize <= 0 {
		config.MaxTemplateSize = defaults.MaxTemplateSize
	}
	if config.MaxTemplateExpressions <= 0 {
		config.MaxTemplateExpressions = defaults.MaxTemplateExpressions
	}
	if config.MaxResolvedStringSize <= 0 {
		config.MaxResolvedStringSize = defaults.MaxResolvedStringSize
	}
	return config
}
