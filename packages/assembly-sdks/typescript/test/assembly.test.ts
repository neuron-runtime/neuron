import { describe, it, expect } from "vitest";
import { Assembly } from "../src/assembly.js";
import { Capability } from "../src/capability.js";
import { connect } from "../src/connection.js";
import { string, number, boolean, record } from "../src/schema.js";
import { createExecutionContext, createSourceContext } from "../src/expression.js";

describe("Assembly", () => {
  it("throws when missing definition", () => {
    const sys = Assembly();
    expect(() => sys.toManifest()).toThrow("Assembly has no definition");
  });

  it("throws when capability is referenced but not defined", () => {
    const sys = Assembly().run({
      kind: "capability",
      capabilityRef: "a",
      bindings: {},
      incomingConditions: [],
    } as any);
    expect(() => sys.toManifest()).toThrow('Capability "a" is referenced in the assembly definition but not defined');
  });

  it("produces a basic manifest with one capability (no registration needed)", () => {
    const a = Capability("a");
    const sys = Assembly().run(a.withParams({}));

    const manifest = sys.toManifest();
    expect(manifest.apiVersion).toBe("neuron/v1");
    expect(manifest.kind).toBe("Assembly");
    expect(manifest.capabilities).toHaveLength(1);
    expect(manifest.capabilities[0]!.name).toBe("a");
    expect(manifest.bindings).toHaveLength(0);
  });

  it("produces a manifest with a sequence and empty binding (auto-passthrough)", () => {
    const a = Capability("a");
    const b = Capability("b");

    const sys = Assembly().run(
      a.bind(b.withParams({}))
    );

    const manifest = sys.toManifest();
    expect(manifest.capabilities).toHaveLength(2);
    expect(manifest.bindings).toHaveLength(1);
    expect(manifest.bindings[0]).toEqual({
      from: "a",
      to: "b",
      mappings: [],
      validations: [],
    });
  });

  it("auto-passthrough maps matching runtime schema fields", () => {
    const a = Capability("a").resultSchema({
      content: string(),
      path: string(),
      ignored: number(),
    });
    const b = Capability("b").paramsSchema({
      content: string().required(),
      path: string().required(),
      count: number(),
    });

    const sys = Assembly().run(a.bind(b));

    expect(sys.toManifest().bindings[0].mappings).toEqual([
      { target: "content", source: { kind: "capabilityResult", capability: "a", path: ["content"] } },
      { target: "path", source: { kind: "capabilityResult", capability: "a", path: ["path"] } },
    ]);
  });

  it("produces a manifest with mappings from withParams bindings", () => {
    const a = Capability("a").resultSchema({ data: string(), valid: boolean() });
    const b = Capability("b").paramsSchema({ data: string().required() });

    const sys = Assembly().run(
      a.bind(
        b.withParams({
          data: a.result.data,
        })
      )
    );

    const manifest = sys.toManifest();
    expect(manifest.bindings[0].mappings).toEqual([
      { target: "data", source: { kind: "capabilityResult", capability: "a", path: ["data"] } },
    ]);
  });

  it("produces validations from bind() conditions", () => {
    const a = Capability("a").resultSchema({ valid: boolean() });
    const b = Capability("b");

    const sys = Assembly().run(
      a.bind(b.withParams({}), {
        when: a.result.valid.equals(true),
        message: "Failed",
      })
    );

    const manifest = sys.toManifest();
    expect(manifest.bindings[0].validations).toEqual([
      { expression: "source.result.valid == true", message: "Failed" },
    ]);
  });

  it("derives parallel execution when multiple capabilities share a source", () => {
    const a = Capability("a").resultSchema({ email: string() });
    const b = Capability("b");
    const c = Capability("c");

    const sys = Assembly().run(
      a
        .bind(b.withParams({ email: a.result.email }))
        .bind(c.withParams({ email: a.result.email }))
    );

    const manifest = sys.toManifest();
    expect(manifest.bindings).toHaveLength(2);
    expect(manifest.bindings.map((binding) => binding.from)).toEqual(["a", "a"]);
    expect(manifest.bindings[0].to).toBe("b");
    expect(manifest.bindings[1].to).toBe("c");
  });

  it("runs independent capabilities as parallel entries with no bindings", () => {
    const a = Capability("a");
    const b = Capability("b");

    const sys = Assembly().run([
      a.withParams({}),
      b.withParams({}),
    ]);

    const manifest = sys.toManifest();
    expect(manifest.capabilities).toHaveLength(2);
    expect(manifest.bindings).toHaveLength(0);
  });

  it("rejects fan-in when a capability reads from multiple sources", () => {
    const a = Capability("a").resultSchema({ x: string() });
    const b = Capability("b").resultSchema({ y: string() });
    const target = Capability("target").paramsSchema({ x: string(), y: string() });

    const sys = Assembly().run([
      a.withParams({}),
      b.withParams({}),
      target.withParams({ x: a.result.x, y: b.result.y }),
    ]);

    expect(() => sys.toManifest()).toThrow(
      /Capability "target" reads inputs from multiple sources \(a, b\)/
    );
  });

  it("deduplicates capabilities referenced multiple times", () => {
    const a = Capability("a");
    const b = Capability("b");

    const sys = Assembly().run(
      a.bind(b.withParams({}))
    );

    const manifest = sys.toManifest();
    expect(manifest.capabilities).toHaveLength(2);
    expect(manifest.bindings).toHaveLength(1);
  });

  it("supports connect() connections with when() conditions", () => {
    const verify = Capability("verify")
      .resultSchema({ verified: boolean(), id: string() });
    const save = Capability("save").paramsSchema({ id: string().required() });

    const source = createSourceContext<{ verified: boolean }>();
    const verifyToSave = connect<{ verified: boolean; id: string }, { id: string }>((src) => ({ id: src.result.id }))
      .when(source.result.verified.equals(true), "Not verified");

    const sys = Assembly().run(
      verify.bind(save.withParams(verifyToSave))
    );

    const manifest = sys.toManifest();
    expect(manifest.bindings[0]).toEqual({
      from: "verify",
      to: "save",
      mappings: [{ target: "id", source: { kind: "capabilityResult", capability: "verify", path: ["id"] } }],
      validations: [
        { expression: "source.result.verified == true", message: "Not verified" },
      ],
    });
  });
});

describe("Assembly with full ecommerce pipeline", () => {
  it("produces the correct manifest for the order-processing pipeline", () => {
    const exec = createExecutionContext<{
      order: {
        items: unknown[];
        total: number;
        currency: string;
        customerEmail: string;
      };
    }>();
    const source = createSourceContext<{
      status: string;
      valid: boolean;
      currency: string;
      customerData: { tier: string; state: string; email: string };
      paymentIntent: { id: string; status: string };
      captureResult: { status: string };
      shipment: { trackingNumber: string; carrier: string };
    }>();
    const validateOrder = Capability("validate-order")
      .runtime({ name: "set" })
      .paramsSchema({ order: record().required() })
      .resultSchema({ valid: boolean(), status: string() });

    const parseOrder = Capability("parse-order")
      .runtime({ name: "set" })
      .paramsSchema({ validationData: record().required() })
      .resultSchema({ currency: string(), items: record() });

    const enrichCustomer = Capability("enrich-customer")
      .runtime({ name: "set" })
      .paramsSchema({ customerId: string().required() })
      .resultSchema({ customerData: record() });

    const calculateTotals = Capability("calculate-totals")
      .runtime({ name: "set" })
      .paramsSchema({
        items: record().required(),
        customerTier: string(),
        shippingState: string(),
        email: string().email(),
      })
      .resultSchema({ total: number() });

    const authorizePayment = Capability("authorize-payment")
      .runtime({ name: "set" })
      .paramsSchema({
        amountCents: number().required(),
        currency: string().required(),
        email: string().email(),
      })
      .resultSchema({ paymentIntent: record() });

    const capturePayment = Capability("capture-payment")
      .runtime({ name: "set" })
      .paramsSchema({ paymentIntentId: string().required() })
      .resultSchema({ captureResult: record() });

    const createShipment = Capability("create-shipment")
      .runtime({ name: "set" })
      .paramsSchema({
        order: record().required(),
        email: string().email(),
      })
      .resultSchema({ shipment: record() });

    const sendConfirmation = Capability("send-confirmation")
      .runtime({ name: "set" })
      .paramsSchema({
        trackingNumber: string(),
        carrier: string(),
        email: string().email(),
        grandTotal: number(),
      })
      .resultSchema({ confirmationSent: boolean() });

    const sys = Assembly().run(
      validateOrder
        .withParams({ order: exec.params.order })
        .bind(parseOrder.withParams({ validationData: source.result.status }), {
          when: source.result.valid.equals(true),
          message: "Order validation failed",
        })
        .bind(enrichCustomer.withParams({ customerId: source.result.currency }))
        .bind(calculateTotals.withParams({
          items: exec.params.order.items,
          customerTier: source.result.customerData.tier,
          shippingState: source.result.customerData.state,
          email: source.result.customerData.email,
        }))
        .bind(authorizePayment.withParams({
          amountCents: exec.params.order.total,
          currency: exec.params.order.currency,
          email: source.result.customerData.email,
        }))
        .bind(capturePayment.withParams({
          paymentIntentId: source.result.paymentIntent.id,
        }), {
          when: source.result.paymentIntent.status.equals("requires_capture"),
          message: "Payment not authorized",
        })
        .bind(createShipment.withParams({
          order: exec.params.order,
          email: exec.params.order.customerEmail,
        }), {
          when: source.result.captureResult.status.equals("succeeded"),
          message: "Payment capture failed",
        })
        .bind(sendConfirmation.withParams({
          trackingNumber: source.result.shipment.trackingNumber,
          carrier: source.result.shipment.carrier,
          email: source.result.customerData.email,
          grandTotal: exec.params.order.total,
        }))
    );

    const manifest = sys.toManifest();

    expect(manifest.apiVersion).toBe("neuron/v1");
    expect(manifest.kind).toBe("Assembly");
    expect(manifest.capabilities).toHaveLength(8);
    expect(manifest.bindings).toHaveLength(7);

    // Unqualified `source.result.*` references from the shared source context
    // are qualified with the binding's source capability, exactly as the
    // canonical compiler does.
    expect(manifest.bindings[0]).toEqual({
      from: "validate-order",
      to: "parse-order",
      mappings: [
        {
          target: "validationData",
          source: { kind: "capabilityResult", capability: "validate-order", path: ["status"] },
        },
      ],
      validations: [
        { expression: "source.result.valid == true", message: "Order validation failed" },
      ],
    });
    expect(manifest.bindings[1]!.mappings).toEqual([
      {
        target: "customerId",
        source: { kind: "capabilityResult", capability: "parse-order", path: ["currency"] },
      },
    ]);
    // Assembly params stay assembly-scoped regardless of the binding.
    expect(manifest.bindings[2]!.mappings).toContainEqual({
      target: "items",
      source: { kind: "assemblyParams", path: ["order", "items"] },
    });
  });
});