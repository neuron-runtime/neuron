export interface Address {
  street: string;
  city: string;
  state: string;
  zip: string;
}

export interface OrderItem {
  sku: string;
  name: string;
  qty: number;
  priceCents: number;
}

export interface OrderInput {
  id: string;
  customerId: string;
  customerEmail: string;
  currency: string;
  items: OrderItem[];
  total: number;
  shippingAddress: Address;
}

export interface AssemblyInput {
  order: OrderInput;
}

export interface ValidateOrderInput {
  order: OrderInput;
}

export interface ValidateOrderOutput {
  order: OrderInput;
}

export interface ParseOrderInput {
  order: OrderInput;
  validationData: unknown;
}

export interface ParseOrderOutput {
  order: OrderInput;
  validationData: unknown;
}

export interface EnrichCustomerInput {
  order: OrderInput;
  customerId: string;
}

export interface EnrichCustomerOutput {
  order: OrderInput;
  customerId: string;
}

export interface CalculateTotalsInput {
  order: OrderInput;
  items: OrderItem[];
  email: string;
}

export interface CalculateTotalsOutput {
  order: OrderInput;
  items: OrderItem[];
  email: string;
}

export interface AuthorizePaymentInput {
  order: OrderInput;
  amountCents: number;
  currency: string;
  email: string;
}

export interface AuthorizePaymentOutput {
  order: OrderInput;
  amountCents: number;
  currency: string;
  email: string;
}

export interface CapturePaymentInput {
  order: OrderInput;
  amountCents: number;
}

export interface CapturePaymentOutput {
  order: OrderInput;
  amountCents: number;
}

export interface CreateShipmentInput {
  order: OrderInput;
  shippingAddress: Address;
  email: string;
}

export interface CreateShipmentOutput {
  order: OrderInput;
  shippingAddress: Address;
  email: string;
}

export interface SendConfirmationInput {
  order: OrderInput;
  email: string;
  grandTotal: number;
}

export interface SendConfirmationOutput {
  order: OrderInput;
  email: string;
  grandTotal: number;
}