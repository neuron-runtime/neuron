using System.Globalization;
using System.Text;
using Neuron.Executor.V1;

namespace Neuron.Executor.Conversion;

/// <summary>
/// Converts between the transport's dynamic <c>Value</c> messages and plain
/// C# values. The conversion table mirrors <c>packages/executor-go/values.go</c>
/// so executors written in either SDK observe identical wire semantics:
/// nil, booleans, strings, all integral/floating point numbers to double,
/// byte arrays to UTF-8 strings, timestamps to RFC 3339 strings, dictionaries
/// to structs, and sequences to lists.
/// </summary>
internal static class ValueConverter
{
    private const string Rfc3339Format = "yyyy-MM-dd'T'HH:mm:ss.fffffffK";

    /// <summary>
    /// Converts a C# value to the transport's dynamic <c>Value</c>. Unsupported
    /// types are rendered as strings, matching the Go SDK fallback.
    /// </summary>
    public static Value ToValue(object? value)
    {
        switch (value)
        {
            case null:
                return new Value { NullValue = NullValue.NullValue };
            case bool b:
                return new Value { BoolValue = b };
            case string s:
                return new Value { StringValue = s };
            case byte[] bytes:
                return new Value { StringValue = Encoding.UTF8.GetString(bytes) };
            case DateTime dt:
                return new Value { StringValue = dt.ToString(Rfc3339Format, CultureInfo.InvariantCulture) };
            case DateTimeOffset dto:
                return new Value { StringValue = dto.UtcDateTime.ToString(Rfc3339Format, CultureInfo.InvariantCulture) };
            case IReadOnlyDictionary<string, object?> map:
                return StructValueFrom(map);
            case IDictionary<string, object?> map:
                return StructValueFrom(map);
            case System.Collections.IEnumerable enumerable:
                return ListValueFrom(enumerable);
        }

        if (TryNormalizeNumber(value, out double number))
        {
            return new Value { NumberValue = number };
        }

        return new Value { StringValue = ToFallbackString(value) };
    }

    /// <summary>
    /// Converts the transport's dynamic <c>Value</c> back to a plain C# value.
    /// Integral doubles decode as <c>long</c>; fractional or out-of-range
    /// doubles decode as <c>double</c>, matching the Go SDK.
    /// </summary>
    public static object? FromValue(Value? value)
    {
        if (value is null)
        {
            return null;
        }

        switch (value.KindCase)
        {
            case Value.KindOneofCase.NullValue:
                return null;
            case Value.KindOneofCase.BoolValue:
                return value.BoolValue;
            case Value.KindOneofCase.StringValue:
                return value.StringValue;
            case Value.KindOneofCase.NumberValue:
                return NormalizeNumber(value.NumberValue);
            case Value.KindOneofCase.ListValue:
                return value.ListValue.Values.Select(FromValue).ToList();
            case Value.KindOneofCase.StructValue:
                return ToObjectDictionary(value.StructValue.Fields);
            default:
                return null;
        }
    }

    /// <summary>
    /// Converts a transport struct message into a plain dictionary.
    /// </summary>
    public static Dictionary<string, object?> ToObjectDictionary(IEnumerable<KeyValuePair<string, Value>> fields)
    {
        int capacity = fields is ICollection<KeyValuePair<string, Value>> collection ? collection.Count : 0;
        var result = new Dictionary<string, object?>(capacity);
        foreach (var (key, v) in fields)
        {
            result[key] = FromValue(v);
        }
        return result;
    }

    private static Value StructValueFrom(IEnumerable<KeyValuePair<string, object?>> map)
    {
        var structMsg = new Struct();
        foreach (var (key, v) in map)
        {
            structMsg.Fields[key] = ToValue(v);
        }
        return new Value { StructValue = structMsg };
    }

    private static Value ListValueFrom(System.Collections.IEnumerable enumerable)
    {
        var listMsg = new ListValue();
        foreach (var item in enumerable)
        {
            listMsg.Values.Add(ToValue(item));
        }
        return new Value { ListValue = listMsg };
    }

    /// <summary>
    /// Maps integral and floating point C# types to <c>double</c>, matching
    /// the Go SDK. Returns <c>false</c> for unsupported types.
    /// </summary>
    private static bool TryNormalizeNumber(object value, out double number)
    {
        switch (value)
        {
            case sbyte n: number = n; return true;
            case byte n: number = n; return true;
            case short n: number = n; return true;
            case ushort n: number = n; return true;
            case int n: number = n; return true;
            case uint n: number = n; return true;
            case long n: number = n; return true;
            case ulong n: number = n; return true;
            case float n: number = n; return true;
            case double n: number = n; return true;
            default:
                number = 0;
                return false;
        }
    }

    /// <summary>
    /// Decodes a transport number. Integral doubles become <c>long</c>;
    /// fractional or out-of-range doubles stay <c>double</c>.
    /// </summary>
    private static object NormalizeNumber(double number)
    {
        if (!double.IsFinite(number)
            || number != Math.Floor(number)
            || number < long.MinValue
            || number > long.MaxValue)
        {
            return number;
        }
        return (long)number;
    }

    private static string ToFallbackString(object value)
    {
        return Convert.ToString(value, CultureInfo.InvariantCulture) ?? string.Empty;
    }
}