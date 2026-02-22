#!/usr/bin/env python3
'''
Calculator MCP Server

This server provides mathematical calculation tools including basic arithmetic,
advanced mathematical functions, trigonometric operations, statistical calculations,
and unit conversions.
'''

from typing import Optional, List, Dict, Any, Union
from enum import Enum
import math
import statistics
from pydantic import BaseModel, Field, field_validator, ConfigDict
from mcp.server.fastmcp import FastMCP

# Initialize the MCP server
mcp = FastMCP("calculator_mcp")

# Enums for operation types and response formats
class OperationType(str, Enum):
    '''Basic arithmetic operations.'''
    ADD = "add"
    SUBTRACT = "subtract"
    MULTIPLY = "multiply"
    DIVIDE = "divide"

class AdvancedOperationType(str, Enum):
    '''Advanced mathematical operations.'''
    POWER = "power"
    SQUARE_ROOT = "square_root"
    LOGARITHM = "logarithm"
    LOGARITHM10 = "logarithm10"
    EXPONENTIAL = "exponential"
    ABSOLUTE = "absolute"
    FLOOR = "floor"
    CEIL = "ceil"

class TrigonometricOperationType(str, Enum):
    '''Trigonometric operations.'''
    SINE = "sine"
    COSINE = "cosine"
    TANGENT = "tangent"
    ARCSINE = "arcsine"
    ARCCOSINE = "arccosine"
    ARCTANGENT = "arctangent"

class UnitType(str, Enum):
    '''Unit types for conversion.'''
    TEMPERATURE = "temperature"
    LENGTH = "length"
    WEIGHT = "weight"
    AREA = "area"
    VOLUME = "volume"

class TemperatureUnit(str, Enum):
    '''Temperature units.'''
    CELSIUS = "celsius"
    FAHRENHEIT = "fahrenheit"
    KELVIN = "kelvin"

class LengthUnit(str, Enum):
    '''Length units.'''
    METER = "meter"
    KILOMETER = "kilometer"
    CENTIMETER = "centimeter"
    MILLIMETER = "millimeter"
    INCH = "inch"
    FOOT = "foot"
    YARD = "yard"
    MILE = "mile"

class WeightUnit(str, Enum):
    '''Weight units.'''
    KILOGRAM = "kilogram"
    GRAM = "gram"
    POUND = "pound"
    OUNCE = "ounce"

class ResponseFormat(str, Enum):
    '''Output format for tool responses.'''
    MARKDOWN = "markdown"
    JSON = "json"

# Pydantic Models for Input Validation
class BasicOperationInput(BaseModel):
    '''Input model for basic arithmetic operations.'''
    model_config = ConfigDict(
        str_strip_whitespace=True,
        validate_assignment=True
    )

    operation: OperationType = Field(..., description="Arithmetic operation to perform")
    a: float = Field(..., description="First operand")
    b: Optional[float] = Field(default=None, description="Second operand (not required for unary operations)")
    response_format: ResponseFormat = Field(default=ResponseFormat.MARKDOWN, description="Output format")

    @field_validator('b')
    @classmethod
    def validate_b_for_division(cls, v: Optional[float], info) -> Optional[float]:
        operation = info.data.get('operation')
        if operation == OperationType.DIVIDE and v == 0:
            raise ValueError("Division by zero is not allowed")
        return v

class AdvancedMathInput(BaseModel):
    '''Input model for advanced mathematical operations.'''
    model_config = ConfigDict(
        str_strip_whitespace=True,
        validate_assignment=True
    )

    operation: AdvancedOperationType = Field(..., description="Advanced mathematical operation")
    value: float = Field(..., description="Input value for the operation")
    exponent: Optional[float] = Field(default=2, description="Exponent for power operation (default: 2)")
    base: Optional[float] = Field(default=math.e, description="Base for logarithm (default: e)")
    response_format: ResponseFormat = Field(default=ResponseFormat.MARKDOWN, description="Output format")

    @field_validator('value')
    @classmethod
    def validate_value(cls, v: float, info) -> float:
        operation = info.data.get('operation')
        if operation == AdvancedOperationType.SQUARE_ROOT and v < 0:
            raise ValueError("Cannot calculate square root of negative number")
        if operation == AdvancedOperationType.LOGARITHM and v <= 0:
            raise ValueError("Logarithm requires positive input value")
        if operation == AdvancedOperationType.LOGARITHM10 and v <= 0:
            raise ValueError("Logarithm base 10 requires positive input value")
        return v

class TrigonometricInput(BaseModel):
    '''Input model for trigonometric operations.'''
    model_config = ConfigDict(
        str_strip_whitespace=True,
        validate_assignment=True
    )

    operation: TrigonometricOperationType = Field(..., description="Trigonometric operation")
    angle: float = Field(..., description="Angle in degrees (for sine/cosine/tangent) or value (for inverse functions)")
    use_radians: bool = Field(default=False, description="Whether the angle is in radians (default: degrees)")
    response_format: ResponseFormat = Field(default=ResponseFormat.MARKDOWN, description="Output format")

    @field_validator('angle')
    @classmethod
    def validate_angle(cls, v: float, info) -> float:
        operation = info.data.get('operation')
        if operation in [TrigonometricOperationType.ARCSINE, TrigonometricOperationType.ARCCOSINE] and (v < -1 or v > 1):
            raise ValueError("Input value must be between -1 and 1 for arcsine and arccosine")
        return v

class StatisticsInput(BaseModel):
    '''Input model for statistical calculations.'''
    model_config = ConfigDict(
        str_strip_whitespace=True,
        validate_assignment=True
    )

    operation: str = Field(..., description="Statistical operation: 'mean', 'median', 'mode', 'stdev', 'variance', 'min', 'max', 'sum'")
    values: List[float] = Field(..., description="List of numerical values", min_length=1)
    response_format: ResponseFormat = Field(default=ResponseFormat.MARKDOWN, description="Output format")

    @field_validator('operation')
    @classmethod
    def validate_operation(cls, v: str) -> str:
        valid_operations = ['mean', 'median', 'mode', 'stdev', 'variance', 'min', 'max', 'sum']
        if v not in valid_operations:
            raise ValueError(f"Operation must be one of: {', '.join(valid_operations)}")
        return v

    @field_validator('values')
    @classmethod
    def validate_values(cls, v: List[float], info) -> List[float]:
        operation = info.data.get('operation')
        if operation == 'mode' and len(v) < 2:
            raise ValueError("Mode calculation requires at least 2 values")
        if operation in ['stdev', 'variance'] and len(v) < 2:
            raise ValueError(f"{operation} calculation requires at least 2 values")
        return v

class UnitConversionInput(BaseModel):
    '''Input model for unit conversions.'''
    model_config = ConfigDict(
        str_strip_whitespace=True,
        validate_assignment=True
    )

    unit_type: UnitType = Field(..., description="Type of unit to convert")
    value: float = Field(..., description="Value to convert")
    from_unit: str = Field(..., description="Source unit")
    to_unit: str = Field(..., description="Target unit")
    response_format: ResponseFormat = Field(default=ResponseFormat.MARKDOWN, description="Output format")

    @field_validator('from_unit', 'to_unit')
    @classmethod
    def validate_units(cls, v: str, info) -> str:
        unit_type = info.data.get('unit_type')
        
        if unit_type == UnitType.TEMPERATURE:
            valid_units = [u.value for u in TemperatureUnit]
        elif unit_type == UnitType.LENGTH:
            valid_units = [u.value for u in LengthUnit]
        elif unit_type == UnitType.WEIGHT:
            valid_units = [u.value for u in WeightUnit]
        else:
            valid_units = []
            
        if v not in valid_units:
            raise ValueError(f"Invalid unit for {unit_type}. Valid units: {', '.join(valid_units)}")
        return v

# Shared utility functions
def _format_result_markdown(operation: str, result: Union[float, str], details: Dict[str, Any] = None) -> str:
    '''Format result as markdown.'''
    lines = [f"# {operation}", ""]
    
    if details:
        for key, value in details.items():
            if isinstance(value, float):
                lines.append(f"- **{key.replace('_', ' ').title()}**: {value:.6f}")
            else:
                lines.append(f"- **{key.replace('_', ' ').title()}**: {value}")
        lines.append("")
    
    if isinstance(result, float):
        lines.append(f"## Result: **{result:.6f}**")
    else:
        lines.append(f"## Result: **{result}**")
    
    return "\n".join(lines)

def _format_result_json(operation: str, result: Union[float, str], details: Dict[str, Any] = None) -> str:
    '''Format result as JSON.'''
    import json
    response = {
        "operation": operation,
        "result": result,
        "details": details or {}
    }
    return json.dumps(response, indent=2)

# Tool definitions
@mcp.tool(
    name="calculator_basic_operation",
    annotations={
        "title": "Basic Arithmetic Operations",
        "readOnlyHint": True,
        "destructiveHint": False,
        "idempotentHint": True,
        "openWorldHint": False
    }
)
async def calculator_basic_operation(params: BasicOperationInput) -> str:
    '''Perform basic arithmetic operations: addition, subtraction, multiplication, and division.

    This tool supports all fundamental arithmetic operations with proper error handling
    for edge cases like division by zero.

    Args:
        params (BasicOperationInput): Validated input parameters containing:
            - operation (OperationType): Arithmetic operation to perform
            - a (float): First operand
            - b (Optional[float]): Second operand (not required for unary operations)
            - response_format (ResponseFormat): Output format (markdown or json)

    Returns:
        str: Formatted result of the arithmetic operation

    Examples:
        - Addition: operation="add", a=5, b=3 → 8
        - Subtraction: operation="subtract", a=10, b=4 → 6
        - Multiplication: operation="multiply", a=7, b=6 → 42
        - Division: operation="divide", a=15, b=3 → 5

    Error Handling:
        - Returns "Error: Division by zero is not allowed" if attempting division by zero
        - Input validation errors are handled by Pydantic model
    '''
    try:
        operation_map = {
            OperationType.ADD: lambda a, b: a + b,
            OperationType.SUBTRACT: lambda a, b: a - b,
            OperationType.MULTIPLY: lambda a, b: a * b,
            OperationType.DIVIDE: lambda a, b: a / b
        }
        
        if params.b is None and params.operation != OperationType.DIVIDE:
            raise ValueError("Second operand is required for this operation")
        
        result = operation_map[params.operation](params.a, params.b)
        
        details = {
            "operation": params.operation.value,
            "a": params.a,
            "b": params.b,
            "expression": f"{params.a} {params.operation.value} {params.b}"
        }
        
        if params.response_format == ResponseFormat.MARKDOWN:
            return _format_result_markdown(f"Basic Arithmetic: {params.operation.value}", result, details)
        else:
            return _format_result_json(f"Basic Arithmetic: {params.operation.value}", result, details)
            
    except ZeroDivisionError:
        return "Error: Division by zero is not allowed"
    except Exception as e:
        return f"Error: {str(e)}"

@mcp.tool(
    name="calculator_advanced_math",
    annotations={
        "title": "Advanced Mathematical Functions",
        "readOnlyHint": True,
        "destructiveHint": False,
        "idempotentHint": True,
        "openWorldHint": False
    }
)
async def calculator_advanced_math(params: AdvancedMathInput) -> str:
    '''Perform advanced mathematical operations: power, square root, logarithm, exponential, etc.

    This tool provides advanced mathematical functions including:
    - Power: value^exponent
    - Square root: √value
    - Logarithm: log_base(value)
    - Logarithm base 10: log10(value)
    - Exponential: e^value
    - Absolute value: |value|
    - Floor: largest integer ≤ value
    - Ceil: smallest integer ≥ value

    Args:
        params (AdvancedMathInput): Validated input parameters containing:
            - operation (AdvancedOperationType): Advanced mathematical operation
            - value (float): Input value for the operation
            - exponent (Optional[float]): Exponent for power operation (default: 2)
            - base (Optional[float]): Base for logarithm (default: e)
            - response_format (ResponseFormat): Output format (markdown or json)

    Returns:
        str: Formatted result of the advanced mathematical operation

    Examples:
        - Power: operation="power", value=2, exponent=3 → 8
        - Square root: operation="square_root", value=16 → 4
        - Logarithm: operation="logarithm", value=10, base=2 → 3.321928
        - Exponential: operation="exponential", value=1 → 2.718282

    Error Handling:
        - Returns "Error: Cannot calculate square root of negative number" for negative inputs
        - Returns "Error: Logarithm requires positive input value" for non-positive inputs
        - Input validation errors are handled by Pydantic model
    '''
    try:
        operation_map = {
            AdvancedOperationType.POWER: lambda v: v ** params.exponent,
            AdvancedOperationType.SQUARE_ROOT: lambda v: math.sqrt(v),
            AdvancedOperationType.LOGARITHM: lambda v: math.log(v, params.base),
            AdvancedOperationType.LOGARITHM10: lambda v: math.log10(v),
            AdvancedOperationType.EXPONENTIAL: lambda v: math.exp(v),
            AdvancedOperationType.ABSOLUTE: lambda v: abs(v),
            AdvancedOperationType.FLOOR: lambda v: math.floor(v),
            AdvancedOperationType.CEIL: lambda v: math.ceil(v)
        }
        
        result = operation_map[params.operation](params.value)
        
        details = {
            "operation": params.operation.value,
            "value": params.value,
        }
        
        if params.operation == AdvancedOperationType.POWER:
            details["exponent"] = params.exponent
            details["expression"] = f"{params.value}^{params.exponent}"
        elif params.operation == AdvancedOperationType.LOGARITHM:
            details["base"] = params.base
            details["expression"] = f"log_{params.base}({params.value})"
        elif params.operation == AdvancedOperationType.SQUARE_ROOT:
            details["expression"] = f"√{params.value}"
        elif params.operation == AdvancedOperationType.EXPONENTIAL:
            details["expression"] = f"e^{params.value}"
        elif params.operation == AdvancedOperationType.ABSOLUTE:
            details["expression"] = f"|{params.value}|"
        
        if params.response_format == ResponseFormat.MARKDOWN:
            return _format_result_markdown(f"Advanced Math: {params.operation.value}", result, details)
        else:
            return _format_result_json(f"Advanced Math: {params.operation.value}", result, details)
            
    except Exception as e:
        return f"Error: {str(e)}"

@mcp.tool(
    name="calculator_trigonometric",
    annotations={
        "title": "Trigonometric Functions",
        "readOnlyHint": True,
        "destructiveHint": False,
        "idempotentHint": True,
        "openWorldHint": False
    }
)
async def calculator_trigonometric(params: TrigonometricInput) -> str:
    '''Perform trigonometric operations: sine, cosine, tangent, and their inverses.

    This tool provides trigonometric functions with support for both degrees and radians.
    Input angles are assumed to be in degrees by default, but can be specified as radians.

    Args:
        params (TrigonometricInput): Validated input parameters containing:
            - operation (TrigonometricOperationType): Trigonometric operation
            - angle (float): Angle in degrees (for sine/cosine/tangent) or value (for inverse functions)
            - use_radians (bool): Whether the angle is in radians (default: degrees)
            - response_format (ResponseFormat): Output format (markdown or json)

    Returns:
        str: Formatted result of the trigonometric operation

    Examples:
        - Sine: operation="sine", angle=30 → 0.5
        - Cosine: operation="cosine", angle=60 → 0.5
        - Arcsine: operation="arcsine", angle=0.5 → 30 (degrees) or 0.523599 (radians)
        - Arccosine: operation="arccosine", angle=0.5 → 60 (degrees) or 1.047198 (radians)

    Error Handling:
        - Returns "Error: Input value must be between -1 and 1 for arcsine and arccosine"
        - Input validation errors are handled by Pydantic model
    '''
    try:
        # Convert angle to radians if needed for forward trigonometric functions
        if params.operation in [TrigonometricOperationType.SINE, TrigonometricOperationType.COSINE, TrigonometricOperationType.TANGENT]:
            if params.use_radians:
                angle_rad = params.angle
                angle_deg = math.degrees(params.angle)
            else:
                angle_deg = params.angle
                angle_rad = math.radians(params.angle)
        else:
            # For inverse functions, the input is already a value (not an angle)
            angle_deg = params.angle
            angle_rad = params.angle
        
        operation_map = {
            TrigonometricOperationType.SINE: lambda: math.sin(angle_rad),
            TrigonometricOperationType.COSINE: lambda: math.cos(angle_rad),
            TrigonometricOperationType.TANGENT: lambda: math.tan(angle_rad),
            TrigonometricOperationType.ARCSINE: lambda: math.asin(angle_rad),
            TrigonometricOperationType.ARCCOSINE: lambda: math.acos(angle_rad),
            TrigonometricOperationType.ARCTANGENT: lambda: math.atan(angle_rad)
        }
        
        result = operation_map[params.operation]()
        
        # Convert result to degrees for inverse functions if not using radians
        if params.operation in [TrigonometricOperationType.ARCSINE, TrigonometricOperationType.ARCCOSINE, TrigonometricOperationType.ARCTANGENT]:
            if not params.use_radians:
                result = math.degrees(result)
        
        details = {
            "operation": params.operation.value,
            "input_value": params.angle,
            "use_radians": params.use_radians
        }
        
        if params.operation in [TrigonometricOperationType.SINE, TrigonometricOperationType.COSINE, TrigonometricOperationType.TANGENT]:
            details["angle_degrees"] = angle_deg
            details["angle_radians"] = angle_rad
            details["expression"] = f"{params.operation.value}({angle_deg}°)"
        else:
            if params.use_radians:
                details["expression"] = f"{params.operation.value}({params.angle})"
            else:
                details["expression"] = f"{params.operation.value}({params.angle}) [result in degrees]"
        
        if params.response_format == ResponseFormat.MARKDOWN:
            return _format_result_markdown(f"Trigonometric: {params.operation.value}", result, details)
        else:
            return _format_result_json(f"Trigonometric: {params.operation.value}", result, details)
            
    except Exception as e:
        return f"Error: {str(e)}"

@mcp.tool(
    name="calculator_statistics",
    annotations={
        "title": "Statistical Calculations",
        "readOnlyHint": True,
        "destructiveHint": False,
        "idempotentHint": True,
        "openWorldHint": False
    }
)
async def calculator_statistics(params: StatisticsInput) -> str:
    '''Perform statistical calculations on a list of numerical values.

    This tool provides common statistical operations including:
    - Mean: average of values
    - Median: middle value when sorted
    - Mode: most frequent value(s)
    - Standard deviation: measure of data dispersion
    - Variance: square of standard deviation
    - Min: smallest value
    - Max: largest value
    - Sum: total of all values

    Args:
        params (StatisticsInput): Validated input parameters containing:
            - operation (str): Statistical operation to perform
            - values (List[float]): List of numerical values
            - response_format (ResponseFormat): Output format (markdown or json)

    Returns:
        str: Formatted result of the statistical calculation

    Examples:
        - Mean: operation="mean", values=[1, 2, 3, 4, 5] → 3.0
        - Median: operation="median", values=[1, 3, 2, 5, 4] → 3.0
        - Standard deviation: operation="stdev", values=[1, 2, 3, 4, 5] → 1.581139
        - Min: operation="min", values=[5, 2, 8, 1, 9] → 1.0

    Error Handling:
        - Returns "Error: Mode calculation requires at least 2 values"
        - Returns "Error: Standard deviation calculation requires at least 2 values"
        - Input validation errors are handled by Pydantic model
    '''
    try:
        operation_map = {
            'mean': lambda v: statistics.mean(v),
            'median': lambda v: statistics.median(v),
            'mode': lambda v: statistics.mode(v) if len(set(v)) < len(v) else "No unique mode",
            'stdev': lambda v: statistics.stdev(v) if len(v) > 1 else 0,
            'variance': lambda v: statistics.variance(v) if len(v) > 1 else 0,
            'min': lambda v: min(v),
            'max': lambda v: max(v),
            'sum': lambda v: sum(v)
        }
        
        result = operation_map[params.operation](params.values)
        
        details = {
            "operation": params.operation,
            "values": params.values,
            "count": len(params.values)
        }
        
        if params.operation == 'mode' and result == "No unique mode":
            details["note"] = "All values are unique, no mode exists"
        
        if params.response_format == ResponseFormat.MARKDOWN:
            return _format_result_markdown(f"Statistics: {params.operation}", result, details)
        else:
            return _format_result_json(f"Statistics: {params.operation}", result, details)
            
    except statistics.StatisticsError as e:
        return f"Error: {str(e)}"
    except Exception as e:
        return f"Error: {str(e)}"

@mcp.tool(
    name="calculator_unit_conversion",
    annotations={
        "title": "Unit Conversion",
        "readOnlyHint": True,
        "destructiveHint": False,
        "idempotentHint": True,
        "openWorldHint": False
    }
)
async def calculator_unit_conversion(params: UnitConversionInput) -> str:
    '''Convert values between different units of measurement.

    This tool supports conversions for:
    - Temperature: celsius, fahrenheit, kelvin
    - Length: meter, kilometer, centimeter, millimeter, inch, foot, yard, mile
    - Weight: kilogram, gram, pound, ounce

    Args:
        params (UnitConversionInput): Validated input parameters containing:
            - unit_type (UnitType): Type of unit to convert
            - value (float): Value to convert
            - from_unit (str): Source unit
            - to_unit (str): Target unit
            - response_format (ResponseFormat): Output format (markdown or json)

    Returns:
        str: Formatted result of the unit conversion

    Examples:
        - Temperature: unit_type="temperature", value=100, from_unit="celsius", to_unit="fahrenheit" → 212
        - Length: unit_type="length", value=1, from_unit="meter", to_unit="inch" → 39.3701
        - Weight: unit_type="weight", value=1, from_unit="kilogram", to_unit="pound" → 2.20462

    Error Handling:
        - Returns "Error: Invalid unit for conversion type"
        - Input validation errors are handled by Pydantic model
    '''
    try:
        # Conversion functions
        def convert_temperature(value: float, from_unit: str, to_unit: str) -> float:
            # Convert to celsius first
            if from_unit == TemperatureUnit.CELSIUS:
                celsius = value
            elif from_unit == TemperatureUnit.FAHRENHEIT:
                celsius = (value - 32) * 5/9
            elif from_unit == TemperatureUnit.KELVIN:
                celsius = value - 273.15
            else:
                raise ValueError(f"Unknown temperature unit: {from_unit}")
            
            # Convert from celsius to target unit
            if to_unit == TemperatureUnit.CELSIUS:
                return celsius
            elif to_unit == TemperatureUnit.FAHRENHEIT:
                return celsius * 9/5 + 32
            elif to_unit == TemperatureUnit.KELVIN:
                return celsius + 273.15
            else:
                raise ValueError(f"Unknown temperature unit: {to_unit}")
        
        def convert_length(value: float, from_unit: str, to_unit: str) -> float:
            # Conversion factors to meters
            to_meter = {
                LengthUnit.METER: 1,
                LengthUnit.KILOMETER: 1000,
                LengthUnit.CENTIMETER: 0.01,
                LengthUnit.MILLIMETER: 0.001,
                LengthUnit.INCH: 0.0254,
                LengthUnit.FOOT: 0.3048,
                LengthUnit.YARD: 0.9144,
                LengthUnit.MILE: 1609.344
            }
            
            # Convert to meters first
            meters = value * to_meter[from_unit]
            
            # Convert from meters to target unit
            return meters / to_meter[to_unit]
        
        def convert_weight(value: float, from_unit: str, to_unit: str) -> float:
            # Conversion factors to kilograms
            to_kilogram = {
                WeightUnit.KILOGRAM: 1,
                WeightUnit.GRAM: 0.001,
                WeightUnit.POUND: 0.45359237,
                WeightUnit.OUNCE: 0.028349523125
            }
            
            # Convert to kilograms first
            kilograms = value * to_kilogram[from_unit]
            
            # Convert from kilograms to target unit
            return kilograms / to_kilogram[to_unit]
        
        # Perform conversion based on unit type
        if params.unit_type == UnitType.TEMPERATURE:
            result = convert_temperature(params.value, params.from_unit, params.to_unit)
        elif params.unit_type == UnitType.LENGTH:
            result = convert_length(params.value, params.from_unit, params.to_unit)
        elif params.unit_type == UnitType.WEIGHT:
            result = convert_weight(params.value, params.from_unit, params.to_unit)
        else:
            return f"Error: Unit type '{params.unit_type}' not yet implemented"
        
        details = {
            "unit_type": params.unit_type.value,
            "value": params.value,
            "from_unit": params.from_unit,
            "to_unit": params.to_unit,
            "expression": f"{params.value} {params.from_unit} → {params.to_unit}"
        }
        
        if params.response_format == ResponseFormat.MARKDOWN:
            return _format_result_markdown(f"Unit Conversion: {params.unit_type.value}", result, details)
        else:
            return _format_result_json(f"Unit Conversion: {params.unit_type.value}", result, details)
            
    except Exception as e:
        return f"Error: {str(e)}"

if __name__ == "__main__":
    mcp.run()