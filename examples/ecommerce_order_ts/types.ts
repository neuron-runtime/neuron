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

export interface OrderParams {
  id: string;
  customerId: string;
  customerEmail: string;
  currency: string;
  items: OrderItem[];
  total: number;
  shippingAddress: Address;
}

export interface AssemblyParams {
  order: OrderParams;
}

export interface ValidateOrderParams {
  order: OrderParams;
}

export interface ValidateOrderResult {
  order: OrderParams;
}

export interface ParseOrderParams {
  order: OrderParams;
  validationData: unknown;
}

export interface ParseOrderResult {
  order: OrderParams;
  validationData: unknown;
}

export interface EnrichCustomerParams {
  order: OrderParams;
  customerId: string;
}

export interface EnrichCustomerResult {
  order: OrderParams;
  customerId: string;
}

export interface CalculateTotalsParams {
  order: OrderParams;
  items: OrderItem[];
  email: string;
}

export interface CalculateTotalsResult {
  order: OrderParams;
  items: OrderItem[];
  email: string;
}

export interface AuthorizePaymentParams {
  order: OrderParams;
  amountCents: number;
  currency: string;
  email: string;
}

export interface AuthorizePaymentResult {
  order: OrderParams;
  amountCents: number;
  currency: string;
  email: string;
}

export interface CapturePaymentParams {
  order: OrderParams;
  amountCents: number;
}

export interface CapturePaymentResult {
  order: OrderParams;
  amountCents: number;
}

export interface CreateShipmentParams {
  order: OrderParams;
  shippingAddress: Address;
  email: string;
}

export interface CreateShipmentResult {
  order: OrderParams;
  shippingAddress: Address;
  email: string;
}

export interface SendConfirmationParams {
  order: OrderParams;
  email: string;
  grandTotal: number;
}

export interface SendConfirmationResult {
  order: OrderParams;
  email: string;
  grandTotal: number;
}