import { type Expressionify } from "@neuron/sdk";
import {
  validateOrder,
  parseOrder,
  enrichCustomer,
  calculateTotals,
  authorizePayment,
  capturePayment,
  createShipment,
  sendConfirmation,
} from "./capabilities/index.js";
import type { AssemblyInput } from "./types.js";

/**
 * Builds the order-processing pipeline from the assembly input.
 *
 * `AssemblyInput` (execution context) is passed in as `data`, so the original
 * order is reachable from any step via `data.order` (compiled to
 * `execution.params.order`). Each capability also forwards the order through
 * its own result, so in-flight mappings read it back from the previous step's
 * result (`source.result.order`).
 *
 * Every capability runtime is `neuron:core:set`, which echoes the capability
 * input (plus config), so a capability only results the data its input
 * carried. Bindings are therefore kept to fields the previous step actually
 * emits, and gateway conditions test order data rather than domain objects no
 * mock step produces.
 */
export function buildPipeline(data: Expressionify<{ order: AssemblyInput["order"] }>) {
  return validateOrder
    .withParams({
      order: data.order,
    })
    .bind(
      parseOrder.withParams({
        order: validateOrder.result.order,
        validationData: validateOrder.result,
      })
    )
    .bind(
      enrichCustomer.withParams({
        order: parseOrder.result.order,
        customerId: parseOrder.result.order.customerId,
      })
    )
    .bind(
      calculateTotals.withParams({
        order: parseOrder.result.order,
        items: parseOrder.result.order.items,
        email: parseOrder.result.order.customerEmail,
      })
    )
    .bind(
      authorizePayment.withParams({
        order: parseOrder.result.order,
        amountCents: parseOrder.result.order.total,
        currency: parseOrder.result.order.currency,
        email: parseOrder.result.order.customerEmail,
      })
    )
    .bind(
      capturePayment.withParams({
        order: authorizePayment.result.order,
        amountCents: authorizePayment.result.amountCents,
      }),
      {
        when: authorizePayment.result.amountCents.greaterThanOrEqualTo(1000),
        message: "Payment not authorized",
      }
    )
    .bind(
      createShipment.withParams({
        order: data.order,
        shippingAddress: data.order.shippingAddress,
        email: data.order.customerEmail,
      }),
      {
        when: capturePayment.result.amountCents.greaterThanOrEqualTo(1000),
        message: "Payment capture failed",
      }
    )
    .bind(
      sendConfirmation.withParams({
        order: createShipment.result.order,
        email: createShipment.result.email,
        grandTotal: createShipment.result.order.total,
      })
    );
}