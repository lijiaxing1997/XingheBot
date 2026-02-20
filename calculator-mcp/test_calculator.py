#!/usr/bin/env python3
'''
Test script for Calculator MCP Server

This script tests the calculator MCP server by importing and calling
the tool functions directly.
'''

import asyncio
from calculator_mcp import (
    calculator_basic_operation,
    calculator_advanced_math,
    calculator_trigonometric,
    calculator_statistics,
    calculator_unit_conversion,
    BasicOperationInput,
    AdvancedMathInput,
    TrigonometricInput,
    StatisticsInput,
    UnitConversionInput,
    OperationType,
    AdvancedOperationType,
    TrigonometricOperationType,
    UnitType,
    ResponseFormat,
    TemperatureUnit,
    LengthUnit,
    WeightUnit
)

async def test_basic_operations():
    '''Test basic arithmetic operations.'''
    print("=== Testing Basic Operations ===")
    
    # Test addition
    params = BasicOperationInput(
        operation=OperationType.ADD,
        a=5,
        b=3,
        response_format=ResponseFormat.MARKDOWN
    )
    result = await calculator_basic_operation(params)
    print("Addition (5 + 3):")
    print(result[:200] + "..." if len(result) > 200 else result)
    print()
    
    # Test division
    params = BasicOperationInput(
        operation=OperationType.DIVIDE,
        a=15,
        b=3,
        response_format=ResponseFormat.JSON
    )
    result = await calculator_basic_operation(params)
    print("Division (15 / 3) JSON:")
    print(result[:200] + "..." if len(result) > 200 else result)
    print()

async def test_advanced_math():
    '''Test advanced mathematical operations.'''
    print("=== Testing Advanced Math ===")
    
    # Test square root
    params = AdvancedMathInput(
        operation=AdvancedOperationType.SQUARE_ROOT,
        value=16,
        response_format=ResponseFormat.MARKDOWN
    )
    result = await calculator_advanced_math(params)
    print("Square root of 16:")
    print(result[:200] + "..." if len(result) > 200 else result)
    print()
    
    # Test power
    params = AdvancedMathInput(
        operation=AdvancedOperationType.POWER,
        value=2,
        exponent=3,
        response_format=ResponseFormat.JSON
    )
    result = await calculator_advanced_math(params)
    print("2^3 JSON:")
    print(result[:200] + "..." if len(result) > 200 else result)
    print()

async def test_trigonometric():
    '''Test trigonometric operations.'''
    print("=== Testing Trigonometric Functions ===")
    
    # Test sine
    params = TrigonometricInput(
        operation=TrigonometricOperationType.SINE,
        angle=30,
        use_radians=False,
        response_format=ResponseFormat.MARKDOWN
    )
    result = await calculator_trigonometric(params)
    print("Sine of 30 degrees:")
    print(result[:200] + "..." if len(result) > 200 else result)
    print()
    
    # Test arcsine
    params = TrigonometricInput(
        operation=TrigonometricOperationType.ARCSINE,
        angle=0.5,
        use_radians=False,
        response_format=ResponseFormat.JSON
    )
    result = await calculator_trigonometric(params)
    print("Arcsine of 0.5 (degrees) JSON:")
    print(result[:200] + "..." if len(result) > 200 else result)
    print()

async def test_statistics():
    '''Test statistical calculations.'''
    print("=== Testing Statistics ===")
    
    # Test mean
    params = StatisticsInput(
        operation="mean",
        values=[1, 2, 3, 4, 5],
        response_format=ResponseFormat.MARKDOWN
    )
    result = await calculator_statistics(params)
    print("Mean of [1, 2, 3, 4, 5]:")
    print(result[:200] + "..." if len(result) > 200 else result)
    print()
    
    # Test standard deviation
    params = StatisticsInput(
        operation="stdev",
        values=[1, 2, 3, 4, 5],
        response_format=ResponseFormat.JSON
    )
    result = await calculator_statistics(params)
    print("Standard deviation JSON:")
    print(result[:200] + "..." if len(result) > 200 else result)
    print()

async def test_unit_conversion():
    '''Test unit conversions.'''
    print("=== Testing Unit Conversions ===")
    
    # Test temperature conversion
    params = UnitConversionInput(
        unit_type=UnitType.TEMPERATURE,
        value=100,
        from_unit=TemperatureUnit.CELSIUS,
        to_unit=TemperatureUnit.FAHRENHEIT,
        response_format=ResponseFormat.MARKDOWN
    )
    result = await calculator_unit_conversion(params)
    print("100°C to Fahrenheit:")
    print(result[:200] + "..." if len(result) > 200 else result)
    print()
    
    # Test length conversion
    params = UnitConversionInput(
        unit_type=UnitType.LENGTH,
        value=1,
        from_unit=LengthUnit.METER,
        to_unit=LengthUnit.INCH,
        response_format=ResponseFormat.JSON
    )
    result = await calculator_unit_conversion(params)
    print("1 meter to inches JSON:")
    print(result[:200] + "..." if len(result) > 200 else result)
    print()

async def main():
    '''Run all tests.'''
    print("Starting Calculator MCP Server Tests...\n")
    
    await test_basic_operations()
    await test_advanced_math()
    await test_trigonometric()
    await test_statistics()
    await test_unit_conversion()
    
    print("All tests completed!")

if __name__ == "__main__":
    asyncio.run(main())