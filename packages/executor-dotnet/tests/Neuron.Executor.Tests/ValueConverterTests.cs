using System.Text;
using Neuron.Executor.Conversion;
using Neuron.Executor.V1;

namespace Neuron.Executor.Tests;

public class ValueConverterTests
{
    [Theory]
    [InlineData(null)]
    [InlineData(true)]
    [InlineData(false)]
    [InlineData("hello")]
    [InlineData(42L)]
    [InlineData(-7L)]
    [InlineData(0.5)]
    public void Round_Trip_Preserves_Simple_Values(object? value)
    {
        object? decoded = ValueConverter.FromValue(ValueConverter.ToValue(value));
        Assert.Equal(value, decoded);
    }

    [Fact]
    public void Byte_Array_Encodes_As_Utf8_String()
    {
        byte[] bytes = "café"u8.ToArray();
        var value = ValueConverter.ToValue(bytes);
        Assert.Equal(Value.KindOneofCase.StringValue, value.KindCase);
        Assert.Equal("café", value.StringValue);
    }

    [Fact]
    public void Dictionary_Round_Trips_Keys_And_Values()
    {
        var input = new Dictionary<string, object?>
        {
            ["title"] = "Neuron",
            ["wordCount"] = 12L,
            ["valid"] = true,
            ["nested"] = new Dictionary<string, object?> { ["items"] = new object?[] { 1L, "two", null } },
        };

        object? decoded = ValueConverter.FromValue(ValueConverter.ToValue(input));

        Assert.IsType<Dictionary<string, object?>>(decoded);
        var map = (Dictionary<string, object?>)decoded!;
        Assert.Equal("Neuron", map["title"]);
        Assert.Equal(12L, map["wordCount"]);
        Assert.Equal(true, map["valid"]);

        var nested = Assert.IsType<Dictionary<string, object?>>(map["nested"]);
        var items = Assert.IsType<List<object?>>(nested["items"]);
        Assert.Equal(1L, items[0]);
        Assert.Equal("two", items[1]);
        Assert.Null(items[2]);
    }

    [Fact]
    public void List_Is_Preferred_From_Number_Only_Sequence_Before_String_Fallback()
    {
        var value = ValueConverter.ToValue(new object?[] { 1L, 2L, 3L });
        Assert.Equal(Value.KindOneofCase.ListValue, value.KindCase);
        Assert.Equal(3, value.ListValue.Values.Count);
    }

    [Fact]
    public void Integral_Double_Decodes_As_Long()
    {
        var value = new Value { NumberValue = 42.0 };
        Assert.Equal(42L, ValueConverter.FromValue(value));
    }

    [Fact]
    public void Fractional_Double_Decodes_As_Double()
    {
        var value = new Value { NumberValue = 0.5 };
        Assert.Equal(0.5, ValueConverter.FromValue(value));
    }

    [Fact]
    public void Out_Of_Range_Double_Stays_Double()
    {
        var value = new Value { NumberValue = 1e20 };
        Assert.Equal(1e20, ValueConverter.FromValue(value));
    }

    [Fact]
    public void DateTime_Encodes_As_Rfc3339_Utc()
    {
        var utc = new DateTime(2026, 9, 12, 10, 30, 0, DateTimeKind.Utc);
        var value = ValueConverter.ToValue(utc);
        Assert.Equal(Value.KindOneofCase.StringValue, value.KindCase);
        Assert.StartsWith("2026-09-12T10:30:00", value.StringValue);
    }

    [Fact]
    public void Null_Value_Decodes_As_Null()
    {
        Assert.Null(ValueConverter.FromValue(new Value { NullValue = NullValue.NullValue }));
        Assert.Null(ValueConverter.FromValue(null));
    }

    [Fact]
    public void Unsupported_Type_Falls_Back_To_String()
    {
        var value = ValueConverter.ToValue(new StringBuilder("fallback"));
        Assert.Equal("fallback", value.StringValue);
    }
}